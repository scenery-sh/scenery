package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

var htmlRootRefRE = regexp.MustCompile(`\b(src|href)="(/[^"]*)"`)

func (s *Server) routerMux() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if cleanRequestPath(req.URL.Path) == "/v1/tls/allow" {
			s.handleTLSAllow(w, req)
			return
		}
		if s.trustedPublicEdgeRequest(req) {
			s.handlePublicEdgeRoute(w, req)
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
		if kind == RoutePathMode {
			if session.RouteManifest.Mode != RouteModePath {
				http.NotFound(w, req)
				return
			}
			manifest := devDomainRouteManifest(session.RouteManifest, normalizeRouteRequestHost(host))
			if len(manifest.PublicRoutes) > 0 {
				if !publicRouteExposed(manifest, "runtime") && isPathModeRuntimePath(req.URL.Path) {
					http.NotFound(w, req)
					return
				}
				manifest = filterExposedRouteRecords(manifest)
			}
			s.handlePathModeRoute(w, req, sessionWithRouteManifest(session, manifest))
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

func (s *Server) handlePublicEdgeRoute(w http.ResponseWriter, req *http.Request) {
	target, ok := s.deployTargetForHost(requestHost(req))
	if !ok {
		http.NotFound(w, req)
		return
	}
	session, ok := s.runningSessionForDeployTarget(target)
	if !ok {
		http.Error(w, "app is not running", http.StatusServiceUnavailable)
		return
	}
	s.handlePublicPathRoute(w, req, session, target)
}

func (s *Server) deployTargetForHost(host string) (DeployTarget, bool) {
	host = normalizeRouteRequestHost(host)
	if host == "" || s == nil {
		return DeployTarget{}, false
	}
	registry, err := LoadDeployRegistry(s.paths.DeployPath)
	if err != nil {
		return DeployTarget{}, false
	}
	for _, target := range registry.Targets {
		if target.Enabled && normalizeRouteRequestHost(target.Domain) == host {
			return target, true
		}
	}
	return DeployTarget{}, false
}

func (s *Server) runningSessionForDeployTarget(target DeployTarget) (Session, bool) {
	if s == nil || s.registry == nil {
		return Session{}, false
	}
	for _, session := range s.registry.FindByAppRoot(target.AppRoot) {
		if session.Status == "running" && filepath.Clean(session.AppRoot) == filepath.Clean(target.AppRoot) && sessionOwnerVerifies(session) {
			return session, true
		}
	}
	return Session{}, false
}

func (s *Server) handlePublicPathRoute(w http.ResponseWriter, req *http.Request, session Session, target DeployTarget) {
	requestPath := cleanRequestPath(req.URL.Path)
	if isPublicRouteBlockedPath(requestPath) {
		http.NotFound(w, req)
		return
	}
	manifest := publicRouteManifest(session, target)
	record, ok := routeForPath(manifest, requestPath)
	if !ok {
		http.NotFound(w, req)
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
		s.proxyBackendWithOptions(w, req, backend, pathProxyOptions(sessionWithRouteManifest(session, manifest), record).withSPAFallback(shouldUseSPAFallback(req)))
		return
	}
	s.proxyBackendWithOptions(w, req, backend, pathProxyOptions(sessionWithRouteManifest(session, manifest), record))
}

func isPublicRouteBlockedPath(value string) bool {
	value = cleanRequestPath(value)
	for _, prefix := range []string{PathModeRuntimePrefix, PathModeDashboardPrefix, "/__scenery"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}

func publicRouteManifest(session Session, target DeployTarget) RouteManifest {
	baseURL := "https://" + strings.ToLower(strings.TrimSpace(target.Domain))
	records := map[string]RouteRecord{}
	root := normalizeRouteName(target.RootService)
	if root != "" && root != RouteDashboard {
		if _, ok := session.Backends[root]; ok {
			records["root"] = RouteRecord{Name: "root", Kind: publicRouteKind(root), URL: joinRouteURL(baseURL, "/"), Path: "/", Backend: root}
		}
	}
	if _, ok := session.Backends[RouteAPI]; ok {
		records[RouteAPI] = RouteRecord{Name: RouteAPI, Kind: RouteAPI, URL: joinRouteURL(baseURL, "/api/"), Path: "/api/", StripPrefix: "/api", Backend: RouteAPI}
	}
	for name := range session.Backends {
		name = normalizeRouteName(name)
		if !isFrontendRouteName(name) {
			continue
		}
		routePath := "/" + name + "/"
		records[name] = RouteRecord{Name: name, Kind: "frontend", URL: joinRouteURL(baseURL, routePath), Path: routePath, StripPrefix: strings.TrimSuffix(routePath, "/"), Backend: name}
	}
	return RouteManifest{
		ArtifactIdentity: routeManifestIdentity(),
		Mode:             RouteModePath,
		BaseURL:          baseURL,
		Root:             root,
		Worktree:         session.RouteManifest.Worktree,
		Routes:           normalizeRouteRecords(records),
	}
}

func publicRouteKind(backend string) string {
	if isFrontendRouteName(backend) {
		return "frontend"
	}
	return backend
}

func sessionWithRouteManifest(session Session, manifest RouteManifest) Session {
	session.RouteManifest = manifest
	return session
}

func publicRouteExposed(manifest RouteManifest, name string) bool {
	for _, exposed := range manifest.PublicRoutes {
		if exposed == name {
			return true
		}
	}
	return false
}

func isPathModeRuntimePath(value string) bool {
	value = cleanRequestPath(value)
	return value == PathModeRuntimePrefix || strings.HasPrefix(value, PathModeRuntimePrefix+"/") || value == "/__scenery" || strings.HasPrefix(value, "/__scenery/")
}

// filterExposedRouteRecords drops route records not named by PublicRoutes so
// the dev domain origin serves only the configured surface; the localhost
// listener keeps the unfiltered manifest.
func filterExposedRouteRecords(manifest RouteManifest) RouteManifest {
	allowed := make(map[string]bool, len(manifest.PublicRoutes))
	for _, name := range manifest.PublicRoutes {
		allowed[name] = true
	}
	routes := make(map[string]RouteRecord, len(manifest.Routes))
	for name, record := range manifest.Routes {
		if allowed[name] {
			routes[name] = record
		}
	}
	manifest.Routes = routes
	return manifest
}

// devDomainRouteManifest rebases a path-mode manifest onto the session's dev
// domain origin so path dispatch, HTML root-ref rewrites, and runtime
// payloads all report https://<host> instead of the localhost port lease.
// The route structure is byte-for-byte today's path mode; only the base
// URL moves.
func devDomainRouteManifest(manifest RouteManifest, host string) RouteManifest {
	baseURL := "https://" + host
	out := manifest
	out.ArtifactIdentity = routeManifestIdentity()
	out.BaseURL = baseURL
	out.DomainHost = host
	out.DomainURL = baseURL
	if len(manifest.Routes) > 0 {
		routes := make(map[string]RouteRecord, len(manifest.Routes))
		for name, record := range manifest.Routes {
			if record.Path != "" {
				record.URL = joinPathModeURL(baseURL, record.Path)
			}
			routes[name] = record
		}
		out.Routes = routes
	}
	return out
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
	htmlPrefix  string
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
	if opts.spaFallback || opts.htmlPrefix != "" {
		proxy.ModifyResponse = func(resp *http.Response) error {
			if opts.spaFallback && resp.StatusCode == http.StatusNotFound && resp.Request != nil && resp.Request.URL != nil {
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
			}
			if opts.htmlPrefix == "" || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
				return nil
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				return err
			}
			body = rewriteHTMLRootRefs(body, opts.htmlPrefix)
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
			resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
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
	case "", RouteAPI, RouteDashboard, "removed-agent-transport":
		return false
	default:
		return true
	}
}

func isProtectedFrontendPath(value string) bool {
	value = cleanRequestPath(value)
	for _, prefix := range []string{PathModeRuntimePrefix, "/__scenery", "/api"} {
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

func (s *Server) trustedPublicEdgeRequest(req *http.Request) bool {
	return s.trustedEdgeRequest(req) && strings.TrimSpace(req.Header.Get("X-Scenery-Public-Edge")) == "1"
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
		writeJSON(w, http.StatusOK, localResponse("scenery.local.health", "sha256:af2ff38e2a1d33b3657300d2a12b8d249a94cb13be536dec4680b030a5275569", map[string]any{
			"status": "ok", "session_id": session.SessionID, "base_url": session.RouteManifest.BaseURL,
		}))
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
	return localResponse("scenery.local.routes", "sha256:cd25c08078a7d79950f1bd1dbf06670499e2c4882ee28a0d0ae04cbfb96402c9", map[string]any{
		"app":        session.BaseAppID,
		"worktree":   session.RouteManifest.Worktree,
		"session_id": session.SessionID,
		"base_url":   session.RouteManifest.BaseURL,
		"routes":     session.RouteManifest.Routes,
	})
}

func localResponse(kind, schemaRevision string, value map[string]any) map[string]any {
	value["kind"] = kind
	value["schema_revision"] = schemaRevision
	value["spec_revision"] = string(spec.CurrentRevision())
	value["producer"] = machine.RuntimeProducer()
	return value
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
	stripPrefix := strings.TrimSpace(record.StripPrefix)
	if strings.TrimSpace(record.Kind) == "frontend" {
		stripPrefix = ""
	}
	htmlPrefix := ""
	if strings.TrimSpace(record.Kind) == "frontend" {
		htmlPrefix = prefix
	}
	return proxyBackendOptions{
		stripPrefix: stripPrefix,
		routePrefix: prefix,
		baseURL:     session.RouteManifest.BaseURL,
		publicURL:   firstNonEmpty(record.URL, joinPathModeURL(session.RouteManifest.BaseURL, record.Path)),
		htmlPrefix:  htmlPrefix,
	}
}

func rewriteHTMLRootRefs(body []byte, prefix string) []byte {
	prefix = strings.TrimRight(cleanRequestPath(prefix), "/")
	if prefix == "" || prefix == "/" {
		return body
	}
	return htmlRootRefRE.ReplaceAllFunc(body, func(match []byte) []byte {
		parts := htmlRootRefRE.FindSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		refPath := string(parts[2])
		if refPath == prefix || strings.HasPrefix(refPath, prefix+"/") || strings.HasPrefix(refPath, "//") {
			return match
		}
		return []byte(string(parts[1]) + "=\"" + prefix + refPath + "\"")
	})
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
	return path != "" && path != "/" && cleanRequestPathPreserveTrailingSlash(req.URL.Path) == path
}

func cleanRequestPathPreserveTrailingSlash(value string) string {
	trailing := strings.HasSuffix(value, "/") && value != "/"
	cleaned := cleanRequestPath(value)
	if trailing && cleaned != "/" && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
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
