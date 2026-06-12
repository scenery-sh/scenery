package agent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"syscall"
)

func (s *Server) routerMux() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if cleanRequestPath(req.URL.Path) == "/v1/tls/allow" {
			s.handleTLSAllow(w, req)
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
	spaFallback bool
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
		if opts.stripPrefix != "" {
			out.URL.Path = strings.TrimPrefix(req.URL.Path, opts.stripPrefix)
			if out.URL.Path == "" {
				out.URL.Path = "/"
			}
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
	case "", RouteAPI, RouteDashboard, RouteGrafana, RouteTemporal, "electric", "removed-agent-transport", "sync":
		return false
	default:
		return true
	}
}

func isProtectedFrontendPath(value string) bool {
	value = cleanRequestPath(value)
	for _, prefix := range []string{"/__scenery", "/api", "/sync"} {
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
