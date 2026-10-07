package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type frontendReadiness struct {
	Milestone  string `json:"milestone"`
	Outcome    string `json:"outcome"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail,omitempty"`
}

type frontendHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

var frontendScriptSource = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)

// A listener is only the first milestone. Require its representative route and
// same-origin client modules advertised by that route before runtime readiness.
// Browser render, HMR application and interaction remain separate evidence.
func probeFrontendHTTP(ctx context.Context, address string, client frontendHTTPClient) (int, error) {
	base, err := url.Parse("http://" + address + "/")
	if err != nil {
		return 0, err
	}
	get := func(target *url.URL, module bool) ([]byte, *url.URL, error) {
		for redirects := 0; redirects <= 5; redirects++ {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
			if err != nil {
				return nil, nil, err
			}
			response, err := client.Do(request)
			if err != nil {
				return nil, nil, err
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
			_ = response.Body.Close()
			if readErr != nil {
				return nil, nil, readErr
			}
			if len(body) > 2<<20 {
				return nil, nil, fmt.Errorf("frontend readiness document exceeds 2 MiB")
			}
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				location := response.Header.Get("Location")
				reference, err := url.Parse(location)
				if err != nil || location == "" {
					return nil, nil, fmt.Errorf("frontend redirect has no valid location")
				}
				target = target.ResolveReference(reference)
				if target.Host != base.Host || target.Scheme != base.Scheme {
					return nil, nil, fmt.Errorf("frontend readiness redirect leaves its managed origin")
				}
				continue
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return nil, nil, fmt.Errorf("frontend route/module returned HTTP %d", response.StatusCode)
			}
			if module && strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
				return nil, nil, fmt.Errorf("frontend client module returned an HTML fallback")
			}
			return body, target, nil
		}
		return nil, nil, fmt.Errorf("frontend readiness redirect limit exceeded")
	}
	document, documentURL, err := get(base, false)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, script := range frontendScriptSource.FindAllStringSubmatch(string(document), -1) {
		reference, err := url.Parse(strings.ReplaceAll(script[1], "&amp;", "&"))
		if err != nil {
			return count, err
		}
		target := documentURL.ResolveReference(reference)
		if target.Host != base.Host || target.Scheme != base.Scheme {
			continue
		}
		if _, _, err := get(target, true); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (process *managedFrontendProcess) readinessObservation() []frontendReadiness {
	process.ownerMu.RLock()
	defer process.ownerMu.RUnlock()
	return append([]frontendReadiness{}, process.readiness...)
}

func (process *managedFrontendProcess) recordReadiness(milestone, outcome, detail string, started time.Time) {
	process.ownerMu.Lock()
	defer process.ownerMu.Unlock()
	process.readiness = append(process.readiness, frontendReadiness{Milestone: milestone, Outcome: outcome, DurationMS: time.Since(started).Milliseconds(), Detail: detail})
}
