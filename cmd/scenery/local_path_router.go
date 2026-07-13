package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/machine"
)

const localPathRouterStateKind = "scenery.local-path-router"
const localPathRouterStateDescriptor = `{"identity":"artifact","state":"local-path-router"}`
const localPathRouterStorageAssetCacheKey = "scenery_path=storage_v2"

var localPathRouterHTMLRootRefRE = regexp.MustCompile(`\b(src|href)="(/[^"]*)"`)
var localPathRouterHTMLDevRootRefRE = regexp.MustCompile(`"(/(?:@fs/|@id/|@react-refresh|@vite/|node_modules/|src/)[^"]*)"`)
var localPathRouterJSRootImportRE = regexp.MustCompile(`((?:from|import)\s*(?:\(\s*)?["'])(/(?:@fs/|@id/|@react-refresh|@vite/|node_modules/|src/)[^"']*)(["'])`)
var localPathRouterJSRootAssetRefRE = regexp.MustCompile(`(["'])(/[^"'\\]*\.(?:avif|gif|ico|jpe?g|png|svg|webp))(["'])`)
var localPathRouterStorageAssetRefRE = regexp.MustCompile(`\b(src|href)="(/storage/assets/[^"?]+\.(?:js|css))"`)

type localPathRouterState struct {
	machine.ArtifactIdentity
	Kind         string    `json:"router_kind"`
	Status       string    `json:"status"`
	SessionID    string    `json:"session_id"`
	AppRoot      string    `json:"app_root"`
	Port         int       `json:"port"`
	URL          string    `json:"url"`
	PID          int       `json:"pid"`
	UpstreamAddr string    `json:"upstream_addr"`
	ConfigPath   string    `json:"config_path,omitempty"`
	LogPath      string    `json:"log_path,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type localPathRouterOptions struct {
	Session          localagent.Session
	PortLease        localagent.PortLease
	EdgeToken        string
	UpstreamAddr     string
	DashboardBackend localagent.Backend
	RedirectURL      string
}

func startLocalPathRouter(ctx context.Context, opts localPathRouterOptions) (func(), error) {
	session := opts.Session
	lease := opts.PortLease
	if lease.Port <= 0 {
		return nil, fmt.Errorf("local path router port lease is missing")
	}
	baseURL := strings.TrimSpace(firstNonEmpty(lease.URL, session.RouteManifest.BaseURL))
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%d", lease.Port)
	}
	upstreamAddr := strings.TrimSpace(opts.UpstreamAddr)
	if upstreamAddr == "" {
		upstreamAddr = localagent.RouterAddrFromEnv()
	}
	token := strings.TrimSpace(opts.EdgeToken)
	if token == "" {
		return nil, fmt.Errorf("local path router token is missing")
	}
	artifacts, err := writeLocalPathRouterArtifacts(session, lease, upstreamAddr, token)
	if err != nil {
		return nil, err
	}
	upstream, err := url.Parse("http://" + upstreamAddr)
	if err != nil {
		return nil, err
	}
	agentClient, _ := localagent.DefaultClient()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(lease.Port)))
	if err != nil {
		return nil, fmt.Errorf("start local path router on %s: %w", baseURL, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	originalDirector := proxy.Director
	setRouteHeaders := func(req *http.Request) {
		originalHost := req.Host
		originalDirector(req)
		req.Host = originalHost
		req.Header.Set("X-Forwarded-Host", originalHost)
		req.Header.Set("X-Forwarded-Proto", "http")
		req.Header.Set("X-Forwarded-Port", strconv.Itoa(lease.Port))
		req.Header.Set("X-Scenery-Local-Route-Mode", string(localagent.RouteModePath))
		req.Header.Set("X-Scenery-Session", session.SessionID)
		req.Header.Set("X-Scenery-Base-URL", baseURL)
		req.Header.Set("X-Scenery-Edge-Token", token)
	}
	proxy.Director = setRouteHeaders
	handler := http.Handler(proxy)
	if strings.TrimSpace(opts.DashboardBackend.Addr) != "" {
		dashboardProxy := reverseProxyForLocalBackend(opts.DashboardBackend)
		dashboardDirector := dashboardProxy.Director
		dashboardProxy.Director = func(req *http.Request) {
			originalHost := req.Host
			originalPath := cleanLocalPathPreserveSlash(req.URL.Path)
			dashboardDirector(req)
			req.Host = originalHost
			req.Header.Set("X-Forwarded-Host", originalHost)
			req.Header.Set("X-Forwarded-Proto", "http")
			req.Header.Set("X-Forwarded-Port", strconv.Itoa(lease.Port))
			req.Header.Set("X-Scenery-Base-URL", baseURL)
			req.Header.Set("X-Scenery-Public-URL", joinDashboardPublicURL(baseURL, req.URL.Path))
			if originalPath == localagent.PathModeRuntimePrefix {
				req.URL.Path = "/__scenery"
			}
			dashboardPrefix := localagent.PathModeDashboardPrefix
			if originalPath == dashboardPrefix || strings.HasPrefix(originalPath, dashboardPrefix+"/") {
				req.URL.Path = strings.TrimPrefix(originalPath, dashboardPrefix)
				if req.URL.Path == "" {
					req.URL.Path = "/"
				}
			}
		}
		handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			rawPath := cleanLocalPathPreserveSlash(req.URL.Path)
			requestPath := cleanLocalPath(req.URL.Path)
			currentSession := session
			refreshCtx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
			currentSession = localPathRouterCurrentSession(refreshCtx, agentClient, session)
			cancel()
			dashboardPrefix := localagent.PathModeDashboardPrefix
			if requestPath == dashboardPrefix && rawPath == dashboardPrefix {
				http.Redirect(w, req, dashboardPrefix+"/", http.StatusMovedPermanently)
				return
			}
			if requestPath == localagent.PathModeRuntimePrefix && isUpgradeRequest(req) {
				if tunnelErr := tunnelLocalBackendUpgrade(w, req, opts.DashboardBackend, "/__scenery"); tunnelErr != nil {
					http.Error(w, tunnelErr.Error(), http.StatusBadGateway)
				}
				return
			}
			if requestPath == localagent.PathModeRuntimePrefix || requestPath == dashboardPrefix || strings.HasPrefix(rawPath, dashboardPrefix+"/") || localPathRouterDashboardAssetPath(requestPath) {
				dashboardProxy.ServeHTTP(w, req)
				return
			}
			if localPathRouterProxySessionBackend(w, req, currentSession, requestPath, lease.Port, baseURL) {
				return
			}
			proxy.ServeHTTP(w, req)
		})
	}
	handler = localPathRouterRedirect(handler, opts.RedirectURL)
	server := &http.Server{Handler: handler}
	done := make(chan error, 1)
	go func() {
		err := server.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			done <- err
			return
		}
		done <- nil
	}()
	state := localPathRouterState{
		ArtifactIdentity: machine.NewArtifactIdentity(localPathRouterStateKind, localPathRouterStateDescriptor),
		Kind:             "builtin",
		Status:           "running",
		SessionID:        session.SessionID,
		AppRoot:          session.AppRoot,
		Port:             lease.Port,
		URL:              baseURL,
		PID:              os.Getpid(),
		UpstreamAddr:     upstreamAddr,
		ConfigPath:       artifacts.configPath,
		LogPath:          artifacts.logPath,
		UpdatedAt:        time.Now().UTC(),
	}
	if err := writeLocalPathRouterState(session.StateRoot, state); err != nil {
		_ = server.Close()
		return nil, err
	}
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = server.Close()
			}
			state.Status = "stopped"
			state.UpdatedAt = time.Now().UTC()
			_ = writeLocalPathRouterState(session.StateRoot, state)
		})
	}
	go func() {
		<-ctx.Done()
		cleanup()
	}()
	return cleanup, nil
}

func localPathRouterRedirect(next http.Handler, target string) http.Handler {
	target = strings.TrimRight(strings.TrimSpace(target), "/")
	if target == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if localPathRouterLocalOnlyPath(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}
		http.Redirect(w, req, target+req.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
}

func localPathRouterLocalOnlyPath(value string) bool {
	requestPath := cleanLocalPath(value)
	for _, prefix := range []string{localagent.PathModeDashboardPrefix, localagent.PathModeRuntimePrefix, "/__scenery"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return localPathRouterDashboardAssetPath(requestPath)
}

func localPathRouterCurrentSession(ctx context.Context, client *localagent.Client, fallback localagent.Session) localagent.Session {
	if client == nil || strings.TrimSpace(fallback.SessionID) == "" {
		return fallback
	}
	sessions, err := client.List(ctx, fallback.AppRoot)
	if err != nil {
		return fallback
	}
	for _, candidate := range sessions {
		if candidate.SessionID == fallback.SessionID {
			return candidate
		}
	}
	return fallback
}

func localPathRouterDashboardAssetPath(requestPath string) bool {
	requestPath = cleanLocalPath(requestPath)
	return strings.HasPrefix(requestPath, "/assets/") ||
		requestPath == "/site.webmanifest" ||
		requestPath == "/favicon.ico" ||
		requestPath == "/apple-touch-icon.png"
}

func isUpgradeRequest(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket") ||
		strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade")
}

func tunnelLocalBackendUpgrade(w http.ResponseWriter, req *http.Request, backend localagent.Backend, rewritePath string) error {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return fmt.Errorf("response writer does not support connection hijacking")
	}
	network := strings.TrimSpace(backend.Network)
	if network == "" {
		network = "tcp"
	}
	backendConn, err := net.Dial(network, backend.Addr)
	if err != nil {
		return err
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		_ = backendConn.Close()
		return err
	}
	out := req.Clone(req.Context())
	out.RequestURI = ""
	out.URL.Scheme = ""
	out.URL.Host = ""
	if rewritePath != "" {
		out.URL.Path = rewritePath
		out.URL.RawPath = ""
	}
	if err := out.Write(backendConn); err != nil {
		_ = backendConn.Close()
		_ = clientConn.Close()
		return err
	}
	go func() {
		_, _ = io.Copy(backendConn, clientConn)
		_ = backendConn.Close()
		_ = clientConn.Close()
	}()
	go func() {
		_, _ = io.Copy(clientConn, backendConn)
		_ = backendConn.Close()
		_ = clientConn.Close()
	}()
	return nil
}

func localPathRouterProxySessionBackend(w http.ResponseWriter, req *http.Request, session localagent.Session, requestPath string, port int, baseURL string) bool {
	record, ok := localPathRouterRouteForSession(session.RouteManifest, requestPath)
	if !ok || record.Name == "root" || record.Backend == "" || record.Backend == localagent.RouteDashboard {
		return false
	}
	if localPathRouterIsStorageRoute(record) && localPathRouterStorageRouteRoot(record, requestPath) {
		http.Redirect(w, req, strings.TrimRight(record.StripPrefix, "/")+"/files", http.StatusFound)
		return true
	}
	backend, ok := session.Backends[record.Backend]
	if !ok || strings.TrimSpace(backend.Addr) == "" {
		return false
	}
	proxy := reverseProxyForLocalBackend(backend)
	director := proxy.Director
	if strings.TrimSpace(record.Kind) == "frontend" {
		proxy.ModifyResponse = func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			if strings.Contains(contentType, "text/html") {
				return localPathRouterRewriteResponseBody(resp, func(body []byte) []byte {
					body = localPathRouterRewriteHTMLRootRefs(body, record.StripPrefix)
					if localPathRouterIsStorageRoute(record) {
						body = localPathRouterRewriteStorageAssetRefs(body)
					}
					return body
				})
			}
			if localPathRouterJavaScriptContentType(contentType) {
				resp.Header.Set("Cache-Control", "no-store")
				return localPathRouterRewriteResponseBody(resp, func(body []byte) []byte {
					body = localPathRouterRewriteJSRootRefs(body, record.StripPrefix)
					if localPathRouterIsStorageRoute(record) {
						body = localPathRouterRewriteStorageRootRefs(body, record.StripPrefix)
					}
					return body
				})
			}
			return nil
		}
	}
	proxy.Director = func(out *http.Request) {
		originalHost := out.Host
		originalPath := cleanLocalPath(out.URL.Path)
		director(out)
		out.Host = originalHost
		out.Header.Set("X-Forwarded-Host", originalHost)
		out.Header.Set("X-Forwarded-Proto", "http")
		out.Header.Set("X-Forwarded-Port", strconv.Itoa(port))
		out.Header.Set("X-Forwarded-Prefix", strings.TrimSpace(record.StripPrefix))
		out.Header.Set("X-Scenery-Route-Prefix", strings.TrimSpace(record.StripPrefix))
		out.Header.Set("X-Scenery-Base-URL", baseURL)
		out.Header.Set("X-Scenery-Public-URL", strings.TrimSpace(record.URL))
		if record.StripPrefix != "" && localPathRouterShouldStripPrefix(record) {
			out.URL.Path = strings.TrimPrefix(originalPath, record.StripPrefix)
			if out.URL.Path == "" {
				out.URL.Path = "/"
			}
		}
	}
	proxy.ServeHTTP(w, req)
	return true
}

func localPathRouterRewriteResponseBody(resp *http.Response, rewrite func([]byte) []byte) error {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	body = rewrite(body)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

func localPathRouterJavaScriptContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "javascript") || strings.Contains(contentType, "ecmascript")
}

func localPathRouterIsStorageRoute(record localagent.RouteRecord) bool {
	return record.Name == "storage" || record.Backend == "storage"
}

func localPathRouterStorageRouteRoot(record localagent.RouteRecord, requestPath string) bool {
	prefix := strings.TrimRight(cleanLocalPath(record.StripPrefix), "/")
	if prefix == "" || prefix == "/" {
		return false
	}
	requestPath = strings.TrimRight(cleanLocalPath(requestPath), "/")
	return requestPath == prefix
}

func localPathRouterShouldStripPrefix(record localagent.RouteRecord) bool {
	if localPathRouterIsStorageRoute(record) {
		return true
	}
	return strings.TrimSpace(record.Kind) != "frontend"
}

func localPathRouterRewriteHTMLRootRefs(body []byte, prefix string) []byte {
	prefix = strings.TrimRight(cleanLocalPath(prefix), "/")
	if prefix == "" || prefix == "/" {
		return body
	}
	body = localPathRouterHTMLRootRefRE.ReplaceAllFunc(body, func(match []byte) []byte {
		parts := localPathRouterHTMLRootRefRE.FindSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		refPath := string(parts[2])
		if refPath == prefix || strings.HasPrefix(refPath, prefix+"/") || strings.HasPrefix(refPath, "//") {
			return match
		}
		return []byte(string(parts[1]) + "=\"" + prefix + refPath + "\"")
	})
	return localPathRouterRewriteDevRootRefs(body, prefix, localPathRouterHTMLDevRootRefRE, 1)
}

func localPathRouterRewriteJSRootRefs(body []byte, prefix string) []byte {
	prefix = strings.TrimRight(cleanLocalPath(prefix), "/")
	if prefix == "" || prefix == "/" {
		return body
	}
	body = localPathRouterRewriteDevRootRefs(body, prefix, localPathRouterJSRootImportRE, 2)
	return localPathRouterRewriteDevRootRefs(body, prefix, localPathRouterJSRootAssetRefRE, 2)
}

func localPathRouterRewriteDevRootRefs(body []byte, prefix string, re *regexp.Regexp, pathIndex int) []byte {
	return re.ReplaceAllFunc(body, func(match []byte) []byte {
		parts := re.FindSubmatch(match)
		if len(parts) <= pathIndex {
			return match
		}
		refPath := string(parts[pathIndex])
		if refPath == prefix || strings.HasPrefix(refPath, prefix+"/") || strings.HasPrefix(refPath, "//") {
			return match
		}
		return bytes.Replace(match, parts[pathIndex], []byte(prefix+refPath), 1)
	})
}

func localPathRouterRewriteStorageRootRefs(body []byte, prefix string) []byte {
	prefix = strings.TrimRight(cleanLocalPath(prefix), "/")
	if prefix == "" || prefix == "/" {
		return body
	}
	replacements := []string{"/ws/9p", "/files", "/dashboard", "/terminal", "/favicon.svg"}
	for _, value := range replacements {
		body = bytes.ReplaceAll(body, []byte(value), []byte(prefix+value))
	}
	regexPrefix := strings.TrimLeft(prefix, "/")
	body = bytes.ReplaceAll(body, []byte(`^\/`+regexPrefix+`/files`), []byte(`^\/`+strings.ReplaceAll(regexPrefix, "/", `\/`)+`\/files`))
	return body
}

func localPathRouterRewriteStorageAssetRefs(body []byte) []byte {
	return localPathRouterStorageAssetRefRE.ReplaceAll(body, []byte(`${1}="${2}?`+localPathRouterStorageAssetCacheKey+`"`))
}

func localPathRouterRouteForSession(manifest localagent.RouteManifest, requestPath string) (localagent.RouteRecord, bool) {
	requestPath = cleanLocalPath(requestPath)
	if requestPath == "/ws/9p" {
		record, ok := manifest.Routes["storage"]
		if ok && localPathRouterIsStorageRoute(record) {
			return record, true
		}
	}
	var best localagent.RouteRecord
	bestLen := -1
	for _, record := range manifest.Routes {
		routePath := cleanLocalPath(record.Path)
		if routePath == "/" {
			continue
		}
		trimmed := strings.TrimSuffix(routePath, "/")
		if requestPath == trimmed || strings.HasPrefix(requestPath, routePath) {
			if len(routePath) > bestLen {
				best = record
				bestLen = len(routePath)
			}
		}
	}
	if bestLen < 0 {
		return localagent.RouteRecord{}, false
	}
	return best, true
}

func reverseProxyForLocalBackend(backend localagent.Backend) *httputil.ReverseProxy {
	target := &url.URL{Scheme: "http", Host: backend.Addr}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if strings.TrimSpace(backend.Network) == "unix" {
		addr := backend.Addr
		target.Host = "unix"
		proxy.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", addr)
			},
		}
	}
	return proxy
}

func cleanLocalPath(value string) string {
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return path.Clean(value)
}

func cleanLocalPathPreserveSlash(value string) string {
	trailing := strings.HasSuffix(value, "/") && value != "/"
	cleaned := cleanLocalPath(value)
	if trailing && cleaned != "/" && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

func joinDashboardPublicURL(baseURL, requestPath string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return strings.TrimSpace(requestPath)
	}
	if requestPath == "" || requestPath == "/" {
		return baseURL + "/"
	}
	return baseURL + "/" + strings.TrimLeft(requestPath, "/")
}

type localPathRouterArtifacts struct {
	configPath string
	logPath    string
}

func writeLocalPathRouterArtifacts(session localagent.Session, lease localagent.PortLease, upstreamAddr, token string) (localPathRouterArtifacts, error) {
	dir := filepath.Join(session.StateRoot, "local-caddy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return localPathRouterArtifacts{}, err
	}
	configPath := filepath.Join(dir, "Caddyfile")
	logPath := filepath.Join(dir, "caddy.log")
	config := localPathCaddyConfig(localPathCaddyConfigOptions{
		Port:         lease.Port,
		SessionID:    session.SessionID,
		BaseURL:      firstNonEmpty(lease.URL, session.RouteManifest.BaseURL),
		UpstreamAddr: upstreamAddr,
		Token:        token,
		AdminSocket:  filepath.Join(dir, "admin.sock"),
	})
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		return localPathRouterArtifacts{}, err
	}
	return localPathRouterArtifacts{configPath: configPath, logPath: logPath}, nil
}

type localPathCaddyConfigOptions struct {
	Port         int
	SessionID    string
	BaseURL      string
	UpstreamAddr string
	Token        string
	AdminSocket  string
}

func localPathCaddyConfig(opts localPathCaddyConfigOptions) string {
	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" && opts.Port > 0 {
		baseURL = fmt.Sprintf("http://localhost:%d", opts.Port)
	}
	return fmt.Sprintf(`{
	admin unix//%s
	auto_https off
}

http://127.0.0.1:%d, http://localhost:%d {
	reverse_proxy %s {
		flush_interval -1
		header_up Host {host}
		header_up X-Forwarded-Proto http
		header_up X-Forwarded-Port %d
		header_up X-Scenery-Local-Route-Mode path
		header_up X-Scenery-Session %s
		header_up X-Scenery-Base-URL %s
		header_up X-Scenery-Edge-Token %s
	}
}
`, opts.AdminSocket, opts.Port, opts.Port, opts.UpstreamAddr, opts.Port, opts.SessionID, baseURL, opts.Token)
}

func writeLocalPathRouterState(stateRoot string, state localPathRouterState) error {
	state.ArtifactIdentity = machine.NewArtifactIdentity(localPathRouterStateKind, localPathRouterStateDescriptor)
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(filepath.Join(stateRoot, "local-caddy.json"), data, 0o644)
}
