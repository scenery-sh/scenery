package agent

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"sort"
	"strings"
	"syscall"
)

func (s *Server) routerMux() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if cleanRequestPath(req.URL.Path) == "/v1/tls/allow" {
			s.handleTLSAllow(w, req)
			return
		}
		if s.trustedLocalPathRequest(req) {
			sessionID := strings.TrimSpace(req.Header.Get("X-Scenery-Session"))
			session, ok := s.registry.Get(sessionID)
			if !ok || session.RouteManifest.Mode != RouteModePath || !sessionOwnerVerifies(session) {
				http.NotFound(w, req)
				return
			}
			s.handlePathModeRoute(w, req, session)
			return
		}
		host := requestHost(req)
		session, kind, ok := s.routeTargetForHost(host)
		if !ok {
			http.NotFound(w, req)
			return
		}
		if kind == RouteDashboard {
			s.handleConsole(w, req, session)
			return
		}
		backend, ok := session.Backends[kind]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if isFrontendSessionBackend(kind) {
			s.handleFrontendRoute(w, req, session, backend)
			return
		}
		s.proxyBackend(w, req, backend, "")
	})
}

func (s *Server) routeTargetForHost(host string) (Session, string, bool) {
	host = normalizeRouteRequestHost(host)
	if host == "" || s == nil || s.registry == nil {
		return Session{}, "", false
	}
	return s.registry.RouteTargetForHost(host)
}

func (s *Server) hasRouteHost(host string) bool {
	_, _, ok := s.routeTargetForHost(host)
	return ok
}

func (s *Server) tlsAllowedHost(host string) bool {
	session, _, ok := s.routeTargetForHost(host)
	if !ok {
		return false
	}
	return sessionOwnerVerifies(session)
}

func sessionOwnerVerifies(session Session) bool {
	ownerPID := firstPositive(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		return false
	}
	owner := session.Owner
	if owner.PID <= 0 {
		owner.PID = ownerPID
	}
	return VerifyOwner(owner) == nil
}

func (s *Server) handleFrontendRoute(w http.ResponseWriter, req *http.Request, session Session, backend Backend) {
	requestPath := cleanRequestPath(req.URL.Path)
	if requestPath == "/__scenery/config" {
		api, ok := session.Backends[RouteAPI]
		if !ok {
			http.NotFound(w, req)
			return
		}
		s.proxyBackend(w, req, api, "")
		return
	}
	if isProtectedFrontendPath(requestPath) {
		http.NotFound(w, req)
		return
	}
	s.proxyBackendWithOptions(w, req, backend, proxyBackendOptions{
		spaFallback: shouldUseSPAFallback(req),
	})
}

func (s *Server) handleConsole(w http.ResponseWriter, req *http.Request, session Session) {
	if s.dashboard.Addr != "" {
		s.proxyBackend(w, req, s.dashboard, "")
		return
	}
	backend, ok := session.Backends[RouteDashboard]
	if !ok {
		http.NotFound(w, req)
		return
	}
	s.proxyBackend(w, req, backend, "")
}

func requestHost(req *http.Request) string {
	host := strings.ToLower(strings.TrimSpace(req.Host))
	if host == "" && req.URL != nil {
		host = strings.ToLower(strings.TrimSpace(req.URL.Host))
	}
	return normalizeRouteRequestHost(host)
}

func normalizeRouteRequestHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

type proxyBackendOptions struct {
	stripPrefix string
	rewritePath string
	spaFallback bool
	routePrefix string
	baseURL     string
	publicURL   string
}

func (s *Server) proxyBackend(w http.ResponseWriter, req *http.Request, backend Backend, stripPrefix string) {
	s.proxyBackendWithOptions(w, req, backend, proxyBackendOptions{stripPrefix: stripPrefix})
}

func (s *Server) proxyBackendWithOptions(w http.ResponseWriter, req *http.Request, backend Backend, opts proxyBackendOptions) {
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
		out.Host = req.Host
		out.Header.Set("X-Forwarded-Host", req.Host)
		out.Header.Set("X-Forwarded-Proto", s.forwardedProto(req))
		out.Header.Set("X-Forwarded-Port", s.forwardedPort(req))
		if opts.rewritePath != "" {
			out.URL.Path = opts.rewritePath
			out.URL.RawPath = ""
		} else if opts.stripPrefix != "" {
			out.URL.Path = strings.TrimPrefix(req.URL.Path, opts.stripPrefix)
			if out.URL.Path == "" {
				out.URL.Path = "/"
			}
		}
		if opts.routePrefix != "" {
			out.Header.Set("X-Forwarded-Prefix", opts.routePrefix)
			out.Header.Set("X-Scenery-Route-Prefix", opts.routePrefix)
		}
		if opts.baseURL != "" {
			out.Header.Set("X-Scenery-Base-URL", opts.baseURL)
		}
		if opts.publicURL != "" {
			out.Header.Set("X-Scenery-Public-URL", opts.publicURL)
		}
	}
	if opts.spaFallback {
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode != http.StatusNotFound || resp.Request == nil || resp.Request.URL == nil {
				return nil
			}
			fallbackReq := resp.Request.Clone(req.Context())
			fallbackReq.URL.Path = "/"
			fallbackReq.URL.RawPath = ""
			fallbackReq.URL.RawQuery = ""
			fallbackResp, err := transport.RoundTrip(fallbackReq)
			if err != nil {
				return nil
			}
			_ = resp.Body.Close()
			resp.StatusCode = fallbackResp.StatusCode
			resp.Status = fallbackResp.Status
			resp.Header = fallbackResp.Header
			resp.Body = fallbackResp.Body
			resp.ContentLength = fallbackResp.ContentLength
			resp.TransferEncoding = fallbackResp.TransferEncoding
			resp.Trailer = fallbackResp.Trailer
			return nil
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		if isBackendUnavailableError(err) {
			// The backend socket is gone or refusing connections — in dev this
			// is almost always a supervised app restart. Tell clients to retry
			// shortly instead of treating it as a hard upstream failure.
			w.Header().Set("Retry-After", "1")
			http.Error(w, "backend restarting, retry shortly", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, req)
}

func isBackendUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

func isFrontendSessionBackend(kind string) bool {
	switch kind {
	case "", RouteAPI, RouteDashboard, "removed-agent-transport", "sync":
		return false
	default:
		return true
	}
}

func isProtectedFrontendPath(value string) bool {
	value = cleanRequestPath(value)
	for _, prefix := range []string{PathModeRuntimePrefix, "/__scenery", "/api", "/sync"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}

func shouldUseSPAFallback(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	if isConcreteAssetPath(req.URL.Path) {
		return false
	}
	accept := strings.ToLower(req.Header.Get("Accept"))
	return strings.Contains(accept, "text/html")
}

func isConcreteAssetPath(value string) bool {
	value = cleanRequestPath(value)
	if value == "/" {
		return false
	}
	for _, prefix := range []string{"/assets/", "/static/", "/public/"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	base := path.Base(value)
	return strings.Contains(base, ".")
}

func cleanRequestPath(value string) string {
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return path.Clean(value)
}

func (s *Server) forwardedProto(req *http.Request) string {
	if s.trustedEdgeRequest(req) {
		if proto := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")); proto != "" {
			return proto
		}
	}
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Server) forwardedPort(req *http.Request) string {
	if s.trustedEdgeRequest(req) {
		if port := strings.TrimSpace(req.Header.Get("X-Forwarded-Port")); port != "" {
			return port
		}
	}
	if _, port, err := net.SplitHostPort(strings.TrimSpace(req.Host)); err == nil && port != "" {
		return port
	}
	if req.TLS != nil {
		return "443"
	}
	return "80"
}

func (s *Server) trustedEdgeRequest(req *http.Request) bool {
	if s == nil || strings.TrimSpace(s.edgeToken) == "" || req == nil {
		return false
	}
	if req.Header.Get("X-Scenery-Edge-Token") != s.edgeToken {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(req.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) trustedLocalPathRequest(req *http.Request) bool {
	if !s.trustedEdgeRequest(req) {
		return false
	}
	return strings.TrimSpace(req.Header.Get("X-Scenery-Local-Route-Mode")) == string(RouteModePath) &&
		strings.TrimSpace(req.Header.Get("X-Scenery-Session")) != ""
}

func (s *Server) handlePathModeRoute(w http.ResponseWriter, req *http.Request, session Session) {
	requestPath := cleanRequestPath(req.URL.Path)
	switch requestPath {
	case PathModeRuntimePrefix + "/health":
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": "scenery.local.health.v1",
			"status":         "ok",
			"session_id":     session.SessionID,
			"base_url":       session.RouteManifest.BaseURL,
		})
		return
	case PathModeRuntimePrefix + "/routes":
		writeJSON(w, http.StatusOK, localRoutesResponse(session))
		return
	case PathModeRuntimePrefix:
		backend := s.dashboard
		if backend.Addr == "" {
			var ok bool
			backend, ok = session.Backends[RouteDashboard]
			if !ok {
				http.NotFound(w, req)
				return
			}
		}
		s.proxyBackendWithOptions(w, req, backend, proxyBackendOptions{
			rewritePath: "/__scenery",
			routePrefix: "/",
			baseURL:     session.RouteManifest.BaseURL,
			publicURL:   joinPathModeURL(session.RouteManifest.BaseURL, PathModeRuntimePrefix),
		})
		return
	case PathModeRuntimePrefix + "/config":
		api, ok := session.Backends[RouteAPI]
		if !ok {
			http.NotFound(w, req)
			return
		}
		s.proxyBackendWithOptions(w, req, api, proxyBackendOptions{
			rewritePath: "/__scenery/config",
			routePrefix: PathModeRuntimePrefix + "/config",
			baseURL:     session.RouteManifest.BaseURL,
			publicURL:   joinPathModeURL(session.RouteManifest.BaseURL, PathModeRuntimePrefix+"/config"),
		})
		return
	}
	record, ok := routeForPath(session.RouteManifest, requestPath)
	if !ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, "unknown Scenery route %s\n\nAvailable routes:\n%s", requestPath, pathRouteList(session.RouteManifest))
		return
	}
	if record.Name == "root" {
		s.handlePathModeRoot(w, req, session)
		return
	}
	if record.Backend == RouteDashboard || record.Kind == "scenery-console" {
		if shouldRedirectPathPrefix(req, record) {
			http.Redirect(w, req, record.Path, http.StatusMovedPermanently)
			return
		}
		backend := s.dashboard
		if backend.Addr == "" {
			var ok bool
			backend, ok = session.Backends[RouteDashboard]
			if !ok {
				http.NotFound(w, req)
				return
			}
		}
		s.proxyBackendWithOptions(w, req, backend, pathProxyOptions(session, record))
		return
	}
	backend, ok := session.Backends[record.Backend]
	if !ok {
		http.NotFound(w, req)
		return
	}
	if isFrontendSessionBackend(record.Backend) {
		if shouldRedirectPathPrefix(req, record) {
			http.Redirect(w, req, record.Path, http.StatusMovedPermanently)
			return
		}
		if isProtectedFrontendPath(strings.TrimPrefix(requestPath, strings.TrimSuffix(record.Path, "/"))) {
			http.NotFound(w, req)
			return
		}
		s.proxyBackendWithOptions(w, req, backend, pathProxyOptions(session, record).withSPAFallback(shouldUseSPAFallback(req)))
		return
	}
	s.proxyBackendWithOptions(w, req, backend, pathProxyOptions(session, record))
}

func localRoutesResponse(session Session) map[string]any {
	return map[string]any{
		"schema_version": "scenery.local.routes.v1",
		"app":            session.BaseAppID,
		"worktree":       session.RouteManifest.Worktree,
		"session_id":     session.SessionID,
		"base_url":       session.RouteManifest.BaseURL,
		"routes":         session.RouteManifest.Routes,
	}
}

func (s *Server) handlePathModeRoot(w http.ResponseWriter, req *http.Request, session Session) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString("<!doctype html>\n<title>Scenery: ")
	b.WriteString(html.EscapeString(session.BaseAppID))
	if session.RouteManifest.Worktree != "" {
		b.WriteString(" ")
		b.WriteString(html.EscapeString(session.RouteManifest.Worktree))
	}
	b.WriteString("</title>\n<h1>")
	b.WriteString(html.EscapeString(session.BaseAppID))
	b.WriteString("</h1>\n")
	if session.RouteManifest.Worktree != "" {
		b.WriteString("<p>Worktree: ")
		b.WriteString(html.EscapeString(session.RouteManifest.Worktree))
		b.WriteString("</p>\n")
	}
	b.WriteString("<p>App root: ")
	b.WriteString(html.EscapeString(session.AppRoot))
	b.WriteString("</p>\n<h2>Services</h2>\n<ul>\n")
	for _, record := range sortedRouteRecords(session.RouteManifest.Routes) {
		if record.Name == "root" || record.Path == "" {
			continue
		}
		b.WriteString("<li><a href=\"")
		b.WriteString(html.EscapeString(record.Path))
		b.WriteString("\">")
		b.WriteString(html.EscapeString(record.Name))
		b.WriteString("</a></li>\n")
	}
	b.WriteString("</ul>\n")
	if req.Method != http.MethodHead {
		_, _ = w.Write([]byte(b.String()))
	}
}

func routeForPath(manifest RouteManifest, requestPath string) (RouteRecord, bool) {
	requestPath = cleanRequestPath(requestPath)
	var best RouteRecord
	bestLen := -1
	for _, record := range manifest.Routes {
		if record.Name == "root" {
			if requestPath == "/" && bestLen < 1 {
				best, bestLen = record, 1
			}
			continue
		}
		if !matchRoutePrefix(record, requestPath) {
			continue
		}
		if len(strings.TrimSuffix(record.Path, "/")) > bestLen {
			best, bestLen = record, len(strings.TrimSuffix(record.Path, "/"))
		}
	}
	if bestLen < 0 {
		return RouteRecord{}, false
	}
	return best, true
}

func matchRoutePrefix(record RouteRecord, requestPath string) bool {
	prefix := strings.TrimSuffix(normalizeRoutePath(record.Path), "/")
	if prefix == "" || prefix == "/" {
		return requestPath == "/"
	}
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

func pathProxyOptions(session Session, record RouteRecord) proxyBackendOptions {
	prefix := strings.TrimSuffix(normalizeRoutePath(record.Path), "/")
	if prefix == "/" {
		prefix = ""
	}
	return proxyBackendOptions{
		stripPrefix: strings.TrimSpace(record.StripPrefix),
		routePrefix: prefix,
		baseURL:     session.RouteManifest.BaseURL,
		publicURL:   firstNonEmpty(record.URL, joinPathModeURL(session.RouteManifest.BaseURL, record.Path)),
	}
}

func (opts proxyBackendOptions) withSPAFallback(enabled bool) proxyBackendOptions {
	opts.spaFallback = enabled
	return opts
}

func shouldRedirectPathPrefix(req *http.Request, record RouteRecord) bool {
	if req == nil || req.URL == nil {
		return false
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	path := strings.TrimSuffix(normalizeRoutePath(record.Path), "/")
	return path != "" && path != "/" && cleanRequestPath(req.URL.Path) == path
}

func pathRouteList(manifest RouteManifest) string {
	var lines []string
	for _, record := range sortedRouteRecords(manifest.Routes) {
		if record.Path != "" {
			lines = append(lines, record.Name+" "+record.Path)
		}
	}
	return strings.Join(lines, "\n")
}

func sortedRouteRecords(records map[string]RouteRecord) []RouteRecord {
	out := make([]RouteRecord, 0, len(records))
	for _, record := range records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func joinPathModeURL(baseURL, routePath string) string {
	return joinRouteURL(baseURL, routePath)
}
