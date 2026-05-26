package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func (s *Server) routerMux() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host := requestHost(req)
		if host == "console.onlava.localhost" {
			s.handleConsole(w, req)
			return
		}
		kind, sessionID, ok := routeHostParts(host)
		if !ok {
			http.NotFound(w, req)
			return
		}
		session, found := s.registry.Get(sessionID)
		if !found {
			http.NotFound(w, req)
			return
		}
		backend, ok := session.Backends[kind]
		if !ok && kind == RouteMCP {
			backend, ok = session.Backends[RouteDashboard]
		}
		if !ok {
			http.NotFound(w, req)
			return
		}
		proxyBackend(w, req, backend, "")
	})
}

func (s *Server) handleConsole(w http.ResponseWriter, req *http.Request) {
	path := strings.Trim(req.URL.Path, "/")
	if path == "" {
		s.serveConsoleIndex(w, req)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] == "s" {
		sessionID := parts[1]
		session, ok := s.registry.Get(sessionID)
		if !ok {
			http.NotFound(w, req)
			return
		}
		backend, ok := session.Backends[RouteDashboard]
		if !ok {
			http.NotFound(w, req)
			return
		}
		strip := "/s/" + sessionID
		proxyBackend(w, req, backend, strip)
		return
	}
	http.NotFound(w, req)
}

func (s *Server) serveConsoleIndex(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>onlava Agent</title></head><body><main><h1>onlava Agent</h1><ul>")
	for _, session := range s.registry.List() {
		href := "/s/" + session.SessionID
		_, _ = fmt.Fprintf(w, "<li><a href=\"%s\">%s</a> <code>%s</code></li>", href, session.SessionID, session.AppRoot)
	}
	_, _ = io.WriteString(w, "</ul></main></body></html>")
}

func requestHost(req *http.Request) string {
	host := strings.ToLower(strings.TrimSpace(req.Host))
	if host == "" && req.URL != nil {
		host = strings.ToLower(strings.TrimSpace(req.URL.Host))
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

func routeHostParts(host string) (kind, sessionID string, ok bool) {
	const suffix = ".onlava.localhost"
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if !strings.HasSuffix(host, suffix) {
		return "", "", false
	}
	prefix := strings.TrimSuffix(host, suffix)
	parts := strings.Split(prefix, ".")
	if len(parts) != 2 {
		return "", "", false
	}
	kind = sanitizeLabel(parts[0])
	sessionID = sanitizeLabel(parts[1])
	if kind == "" || sessionID == "" {
		return "", "", false
	}
	return kind, sessionID, true
}

func proxyBackend(w http.ResponseWriter, req *http.Request, backend Backend, stripPrefix string) {
	target := &url.URL{Scheme: "http", Host: backend.Addr}
	transport := http.DefaultTransport
	if backend.Network == "unix" {
		target.Host = "unix"
		addr := backend.Addr
		transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", addr)
			},
		}
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	originalDirector := proxy.Director
	proxy.Director = func(out *http.Request) {
		originalDirector(out)
		out.Host = target.Host
		if stripPrefix != "" {
			out.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			if out.URL.Path == "" {
				out.URL.Path = "/"
			}
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, req)
}
