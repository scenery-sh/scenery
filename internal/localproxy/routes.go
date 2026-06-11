package localproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type routeTable []proxyRoute

type proxyRoute struct {
	host        string
	path        string
	upstream    string
	rewriteHost bool
	proxy       *httputil.ReverseProxy
}

func proxyRoutes(cfg Config) (routeTable, error) {
	apiHost := resolvedHost(cfg.APIHost, cfg.Workspace, "api")
	consoleHost := resolvedHost(cfg.ConsoleHost, cfg.Workspace, "console")
	temporalHost := resolvedHost(cfg.TemporalHost, cfg.Workspace, "temporal")
	grafanaHost := resolvedHost(cfg.GrafanaHost, cfg.Workspace, "grafana")

	var routes routeTable
	appendRoute := func(host, upstream string, rewriteHost bool, path string) error {
		route, err := newProxyRoute(host, upstream, rewriteHost, path)
		if err != nil {
			return err
		}
		if route != nil {
			routes = append(routes, *route)
		}
		return nil
	}

	if err := appendRoute(apiHost, cfg.APIUpstream, false, ""); err != nil {
		return nil, err
	}
	if cfg.DashboardUpstream != "" {
		if err := appendRoute(consoleHost, cfg.DashboardUpstream, false, ""); err != nil {
			return nil, err
		}
	}
	if cfg.TemporalUpstream != "" {
		if err := appendRoute(temporalHost, cfg.TemporalUpstream, false, ""); err != nil {
			return nil, err
		}
	}
	if cfg.GrafanaUpstream != "" {
		if err := appendRoute(grafanaHost, cfg.GrafanaUpstream, false, ""); err != nil {
			return nil, err
		}
	}
	for _, frontend := range cfg.Frontends {
		if frontend.Host == "" || frontend.Upstream == "" {
			continue
		}
		if cfg.APIUpstream != "" {
			if err := appendRoute(frontend.Host, cfg.APIUpstream, false, "/__scenery/config"); err != nil {
				return nil, err
			}
		}
		if err := appendRoute(frontend.Host, frontend.Upstream, true, ""); err != nil {
			return nil, err
		}
	}
	return routes, nil
}

func newProxyRoute(host, upstream string, rewriteHost bool, path string) (*proxyRoute, error) {
	host = normalizeHost(host)
	upstream = normalizeUpstream(upstream)
	if host == "" || upstream == "" {
		return nil, nil
	}
	target := &url.URL{Scheme: "http", Host: upstream}
	route := &proxyRoute{
		host:        host,
		path:        path,
		upstream:    upstream,
		rewriteHost: rewriteHost,
	}
	route.proxy = newReverseProxy(target, rewriteHost)
	return route, nil
}

func newReverseProxy(target *url.URL, rewriteHost bool) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			if rewriteHost {
				req.Out.Host = target.Host
			} else {
				req.Out.Host = req.In.Host
			}
			req.Out.Header["X-Forwarded-For"] = req.In.Header["X-Forwarded-For"]
			req.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "local proxy upstream unavailable", http.StatusBadGateway)
		},
	}
	return proxy
}

func (t routeTable) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := t.match(r)
	if route == nil {
		http.NotFound(w, r)
		return
	}
	route.proxy.ServeHTTP(w, r)
}

func (t routeTable) match(r *http.Request) *proxyRoute {
	host := requestHost(r.Host)
	for i := range t {
		route := &t[i]
		if route.host != host {
			continue
		}
		if route.path != "" && route.path != r.URL.Path {
			continue
		}
		return route
	}
	return nil
}

func (t routeTable) hasHost(host string) bool {
	host = requestHost(host)
	for _, route := range t {
		if route.host == host {
			return true
		}
	}
	return false
}

func routeSubjects(cfg Config) []string {
	subjects := []string{}
	add := func(host string) {
		if host == "" {
			return
		}
		if slices.Contains(subjects, host) {
			return
		}
		subjects = append(subjects, host)
	}
	add(resolvedHost(cfg.APIHost, cfg.Workspace, "api"))
	if cfg.DashboardUpstream != "" {
		add(resolvedHost(cfg.ConsoleHost, cfg.Workspace, "console"))
	}
	if cfg.TemporalUpstream != "" {
		add(resolvedHost(cfg.TemporalHost, cfg.Workspace, "temporal"))
	}
	if cfg.GrafanaUpstream != "" {
		add(resolvedHost(cfg.GrafanaHost, cfg.Workspace, "grafana"))
	}
	for _, frontend := range cfg.Frontends {
		if frontend.Upstream != "" {
			add(frontend.Host)
		}
	}
	return subjects
}

type redirectHandler struct {
	routes    routeTable
	httpsPort int
}

func (h redirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := requestHost(r.Host)
	if !h.routes.hasHost(host) {
		http.NotFound(w, r)
		return
	}
	location := "https://" + hostForURL(host, h.httpsPort) + r.URL.RequestURI()
	http.Redirect(w, r, location, http.StatusPermanentRedirect)
}

func requestHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

func hostForURL(host string, port int) string {
	if port == defaultHTTPSPort {
		return host
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}
	return fmt.Sprintf("%s:%d", host, port)
}
