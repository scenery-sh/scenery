package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"

	localagent "scenery.sh/internal/agent"
)

const detachedDevReadinessBodyLimit = 1 << 20

func probeDetachedDevRoutes(ctx context.Context, session localagent.Session) error {
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	names := make([]string, 0, len(session.RouteManifest.Routes))
	for name, route := range session.RouteManifest.Routes {
		if strings.TrimSpace(route.URL) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		route := session.RouteManifest.Routes[name]
		assetURL, err := probeDetachedDevURL(ctx, client, route.URL, strings.TrimSpace(route.Kind) == "frontend", false)
		if err != nil {
			return fmt.Errorf("route %s not reachable: %w", name, err)
		}
		if assetURL == "" {
			continue
		}
		if _, err := probeDetachedDevURL(ctx, client, assetURL, false, true); err != nil {
			return fmt.Errorf("frontend %s asset not reachable: %w", name, err)
		}
	}
	return nil
}

func probeDetachedDevURL(ctx context.Context, client *http.Client, rawURL string, findAsset, requireSuccess bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError || requireSuccess && resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	if !findAsset || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return "", nil
	}
	return firstDetachedDevAssetURL(resp.Request.URL, io.LimitReader(resp.Body, detachedDevReadinessBodyLimit))
}

func firstDetachedDevAssetURL(base *url.URL, body io.Reader) (string, error) {
	tokenizer := html.NewTokenizer(body)
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return "", err
			}
			return "", nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			attribute := ""
			switch token.Data {
			case "script":
				attribute = "src"
			case "link":
				if !htmlTokenAttributeContains(token, "rel", "stylesheet") {
					continue
				}
				attribute = "href"
			default:
				continue
			}
			for _, attr := range token.Attr {
				if attr.Key != attribute || strings.TrimSpace(attr.Val) == "" {
					continue
				}
				ref, err := url.Parse(strings.TrimSpace(attr.Val))
				if err != nil || (ref.Scheme != "" && ref.Scheme != "http" && ref.Scheme != "https") {
					continue
				}
				return base.ResolveReference(ref).String(), nil
			}
		}
	}
}

func htmlTokenAttributeContains(token html.Token, key, value string) bool {
	for _, attr := range token.Attr {
		if attr.Key == key {
			for _, field := range strings.Fields(strings.ToLower(attr.Val)) {
				if field == value {
					return true
				}
			}
		}
	}
	return false
}
