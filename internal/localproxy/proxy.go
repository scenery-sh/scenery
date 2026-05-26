package localproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPPort  = 80
	defaultHTTPSPort = 443
)

type Config struct {
	Workspace         string
	APIHost           string
	ConsoleHost       string
	MCPHost           string
	TemporalHost      string
	GrafanaHost       string
	Frontends         []FrontendConfig
	APIUpstream       string
	DashboardUpstream string
	TemporalUpstream  string
	GrafanaUpstream   string
	HTTPPort          int
	HTTPSPort         int
	SkipInstallTrust  bool
	Verbose           bool
}

type FrontendConfig struct {
	Name     string
	Host     string
	Root     string
	Upstream string
}

type Routes struct {
	APIHost      string
	ConsoleHost  string
	MCPHost      string
	TemporalHost string
	GrafanaHost  string
	Frontends    map[string]FrontendRoute
	APIURL       string
	ConsoleURL   string
	MCPBaseURL   string
	TemporalURL  string
	GrafanaURL   string
}

type FrontendRoute struct {
	Name string
	Host string
	URL  string
}

type Proxy struct {
	routes Routes

	httpsServer *http.Server
	httpServer  *http.Server
	httpsLn     net.Listener
	httpLn      net.Listener

	closeOnce sync.Once
	wg        sync.WaitGroup
	mu        sync.Mutex
	serveErrs []error
}

func Enabled() bool {
	return envBool("ONLAVA_LOCAL_PROXY", false)
}

func HTTPPort() int {
	return envInt("ONLAVA_LOCAL_PROXY_HTTP_PORT", defaultHTTPPort)
}

func HTTPSPort() int {
	return envInt("ONLAVA_LOCAL_PROXY_HTTPS_PORT", defaultHTTPSPort)
}

func SkipInstallTrust() bool {
	return envBool("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", true)
}

func FrontendOverride(name string) string {
	value := strings.TrimSpace(os.Getenv("ONLAVA_FRONTEND_" + frontendEnvName(name) + "_ADDR"))
	if value == "" {
		return ""
	}
	return normalizeUpstream(value)
}

func DiscoverWorkspace(root, fallback string) string {
	label := sanitizeLabel(filepath.Base(strings.TrimSpace(root)))
	if label != "" {
		return label
	}
	return sanitizeLabel(fallback)
}

func DiscoverFrontendUpstream(appRoot string, frontend FrontendConfig) string {
	if envBool("ONLAVA_DISABLE_FRONTEND_PROXY", false) {
		return ""
	}
	if override := FrontendOverride(frontend.Name); override != "" {
		return override
	}
	if upstream := normalizeUpstream(frontend.Upstream); upstream != "" {
		return upstream
	}
	frontendRoot := frontendRootPath(appRoot, frontend)
	if frontendRoot == "" {
		return ""
	}
	viteCandidates := []string{
		filepath.Join(frontendRoot, "vite.config.ts"),
		filepath.Join(frontendRoot, "vite.config.js"),
		filepath.Join(frontendRoot, "vite.config.mts"),
		filepath.Join(frontendRoot, "vite.config.mjs"),
	}
	for _, path := range viteCandidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		port := parseVitePort(data)
		if port == 0 {
			port = 5173
		}
		return discoverReachableLoopbackUpstream(port)
	}
	return ""
}

func ResolveFrontends(appRoot string, frontends []FrontendConfig) []FrontendConfig {
	resolved := make([]FrontendConfig, 0, len(frontends))
	for _, frontend := range frontends {
		frontend.Name = sanitizeLabel(frontend.Name)
		frontend.Host = normalizeHost(frontend.Host)
		frontend.Root = strings.TrimSpace(frontend.Root)
		frontend.Upstream = DiscoverFrontendUpstream(appRoot, frontend)
		if frontend.Upstream == "" {
			continue
		}
		resolved = append(resolved, frontend)
	}
	return resolved
}

func frontendRootPath(appRoot string, frontend FrontendConfig) string {
	appRoot = strings.TrimSpace(appRoot)
	root := strings.TrimSpace(frontend.Root)
	if root == "" && frontend.Name != "" {
		root = filepath.Join("apps", frontend.Name)
	}
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	if appRoot == "" {
		return filepath.Clean(root)
	}
	return filepath.Join(appRoot, root)
}

func BuildConfig(cfg Config) Config {
	cfg.APIUpstream = normalizeUpstream(cfg.APIUpstream)
	cfg.DashboardUpstream = normalizeUpstream(cfg.DashboardUpstream)
	cfg.TemporalUpstream = normalizeUpstream(cfg.TemporalUpstream)
	cfg.GrafanaUpstream = normalizeUpstream(cfg.GrafanaUpstream)
	for i := range cfg.Frontends {
		cfg.Frontends[i].Name = sanitizeLabel(cfg.Frontends[i].Name)
		cfg.Frontends[i].Host = normalizeHost(cfg.Frontends[i].Host)
		cfg.Frontends[i].Root = strings.TrimSpace(cfg.Frontends[i].Root)
		cfg.Frontends[i].Upstream = normalizeUpstream(cfg.Frontends[i].Upstream)
	}
	cfg.HTTPPort = HTTPPort()
	cfg.HTTPSPort = HTTPSPort()
	cfg.SkipInstallTrust = SkipInstallTrust()
	return cfg
}

func PreviewRoutes(cfg Config) Routes {
	return routesFor(normalizeConfig(BuildConfig(cfg)))
}

func Start(cfg Config) (*Proxy, error) {
	cfg = normalizeConfig(cfg)
	if cfg.APIUpstream == "" {
		return nil, fmt.Errorf("local proxy requires an API upstream")
	}
	routes := routesFor(cfg)
	if routes.APIHost == "" {
		return nil, fmt.Errorf("local proxy requires an API host or workspace label")
	}
	if cfg.DashboardUpstream != "" && (routes.ConsoleHost == "" || routes.MCPHost == "") {
		return nil, fmt.Errorf("local proxy requires console and mcp hosts when dashboard routing is enabled")
	}
	for _, frontend := range cfg.Frontends {
		if frontend.Upstream != "" && frontend.Host == "" {
			return nil, fmt.Errorf("local proxy requires frontend %q to define a host", frontend.Name)
		}
	}

	routeTable, err := proxyRoutes(cfg)
	if err != nil {
		return nil, err
	}
	certs, err := prepareLocalCertificates(routeSubjects(cfg))
	if err != nil {
		return nil, fmt.Errorf("prepare local HTTPS certificates: %w", err)
	}
	if !cfg.SkipInstallTrust {
		trusted, err := localCATrusted(certs.CAPath)
		if err != nil && cfg.Verbose {
			log.Printf("local HTTPS proxy trust check failed: %v", err)
		}
		if !trusted {
			if err := installLocalCATrust(certs.CAPath); err != nil {
				log.Printf("local HTTPS proxy trust install skipped: %v", err)
			}
		}
	}

	httpsLn, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.HTTPSPort))
	if err != nil {
		return nil, fmt.Errorf("listen local HTTPS proxy: %w", err)
	}
	var httpLn net.Listener
	if cfg.HTTPPort != cfg.HTTPSPort {
		httpLn, err = net.Listen("tcp", fmt.Sprintf(":%d", cfg.HTTPPort))
		if err != nil {
			log.Printf("local HTTPS proxy HTTP redirect unavailable: %v", err)
		}
	}

	httpsServer := &http.Server{
		Handler: routeTable,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{certs.Leaf},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
		},
		ErrorLog: serverErrorLog(cfg.Verbose),
	}
	var httpServer *http.Server
	if httpLn != nil {
		httpServer = &http.Server{
			Handler:  redirectHandler{routes: routeTable, httpsPort: cfg.HTTPSPort},
			ErrorLog: serverErrorLog(cfg.Verbose),
		}
	}

	p := &Proxy{
		routes:      routes,
		httpsServer: httpsServer,
		httpServer:  httpServer,
		httpsLn:     httpsLn,
		httpLn:      httpLn,
	}
	p.serve("https", httpsServer, tls.NewListener(httpsLn, httpsServer.TLSConfig))
	if httpServer != nil {
		p.serve("http", httpServer, httpLn)
	}
	return p, nil
}

func (p *Proxy) Close() error {
	if p == nil {
		return nil
	}
	var closeErr error
	p.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var errs []error
		if p.httpServer != nil {
			if err := p.httpServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs = append(errs, err)
			}
		}
		if p.httpsServer != nil {
			if err := p.httpsServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs = append(errs, err)
			}
		}
		if p.httpLn != nil {
			if err := p.httpLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, err)
			}
		}
		if p.httpsLn != nil {
			if err := p.httpsLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, err)
			}
		}

		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			errs = append(errs, fmt.Errorf("timed out closing local HTTPS proxy"))
		}

		p.mu.Lock()
		errs = append(errs, p.serveErrs...)
		p.mu.Unlock()
		closeErr = errors.Join(errs...)
	})
	return closeErr
}

func (p *Proxy) Routes() Routes {
	if p == nil {
		return Routes{}
	}
	return p.routes
}

func (p *Proxy) serve(name string, server *http.Server, ln net.Listener) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			p.mu.Lock()
			p.serveErrs = append(p.serveErrs, fmt.Errorf("%s local proxy server: %w", name, err))
			p.mu.Unlock()
		}
	}()
}

func ConsoleAppURL(routes Routes, appID string) string {
	if routes.ConsoleURL == "" {
		return ""
	}
	return routes.ConsoleURL + "/" + url.PathEscape(appID)
}

func MCPSSEURL(routes Routes, appID string) string {
	if routes.MCPBaseURL == "" {
		return ""
	}
	return routes.MCPBaseURL + "/sse?appID=" + url.QueryEscape(appID)
}

func normalizeConfig(cfg Config) Config {
	cfg.Workspace = sanitizeLabel(cfg.Workspace)
	cfg.APIHost = normalizeHost(cfg.APIHost)
	cfg.ConsoleHost = normalizeHost(cfg.ConsoleHost)
	cfg.MCPHost = normalizeHost(cfg.MCPHost)
	cfg.TemporalHost = normalizeHost(cfg.TemporalHost)
	cfg.GrafanaHost = normalizeHost(cfg.GrafanaHost)
	if cfg.HTTPPort <= 0 {
		cfg.HTTPPort = defaultHTTPPort
	}
	if cfg.HTTPSPort <= 0 {
		cfg.HTTPSPort = defaultHTTPSPort
	}
	cfg.APIUpstream = normalizeUpstream(cfg.APIUpstream)
	cfg.DashboardUpstream = normalizeUpstream(cfg.DashboardUpstream)
	cfg.TemporalUpstream = normalizeUpstream(cfg.TemporalUpstream)
	cfg.GrafanaUpstream = normalizeUpstream(cfg.GrafanaUpstream)
	for i := range cfg.Frontends {
		cfg.Frontends[i].Name = sanitizeLabel(cfg.Frontends[i].Name)
		cfg.Frontends[i].Host = normalizeHost(cfg.Frontends[i].Host)
		cfg.Frontends[i].Root = strings.TrimSpace(cfg.Frontends[i].Root)
		cfg.Frontends[i].Upstream = normalizeUpstream(cfg.Frontends[i].Upstream)
	}
	return cfg
}

func routesFor(cfg Config) Routes {
	apiHost := resolvedHost(cfg.APIHost, cfg.Workspace, "api")
	consoleHost := resolvedHost(cfg.ConsoleHost, cfg.Workspace, "console")
	mcpHost := resolvedHost(cfg.MCPHost, cfg.Workspace, "mcp")
	temporalHost := resolvedHost(cfg.TemporalHost, cfg.Workspace, "temporal")
	grafanaHost := resolvedHost(cfg.GrafanaHost, cfg.Workspace, "grafana")
	routes := Routes{
		APIHost:      apiHost,
		ConsoleHost:  consoleHost,
		MCPHost:      mcpHost,
		TemporalHost: temporalHost,
		GrafanaHost:  grafanaHost,
		Frontends:    map[string]FrontendRoute{},
	}
	if apiHost != "" {
		routes.APIURL = hostURL(apiHost, cfg.HTTPSPort)
	}
	if cfg.DashboardUpstream != "" {
		if consoleHost != "" {
			routes.ConsoleURL = hostURL(consoleHost, cfg.HTTPSPort)
		}
		if mcpHost != "" {
			routes.MCPBaseURL = hostURL(mcpHost, cfg.HTTPSPort)
		}
	}
	if cfg.TemporalUpstream != "" && temporalHost != "" {
		routes.TemporalURL = hostURL(temporalHost, cfg.HTTPSPort)
	}
	if cfg.GrafanaUpstream != "" && grafanaHost != "" {
		routes.GrafanaURL = hostURL(grafanaHost, cfg.HTTPSPort)
	}
	for _, frontend := range cfg.Frontends {
		if frontend.Host == "" || frontend.Upstream == "" {
			continue
		}
		routes.Frontends[frontend.Name] = FrontendRoute{
			Name: frontend.Name,
			Host: frontend.Host,
			URL:  hostURL(frontend.Host, cfg.HTTPSPort),
		}
	}
	return routes
}

func frontendEnvName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func hostURL(host string, httpsPort int) string {
	if httpsPort == defaultHTTPSPort {
		return "https://" + host
	}
	return fmt.Sprintf("https://%s:%d", host, httpsPort)
}

func normalizeUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err == nil && u.Host != "" {
			return normalizeUpstream(u.Host)
		}
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return value
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func normalizeHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err == nil && u.Host != "" {
			value = u.Host
		}
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

func resolvedHost(explicit, workspace, subdomain string) string {
	if explicit = normalizeHost(explicit); explicit != "" {
		return explicit
	}
	workspace = sanitizeLabel(workspace)
	if workspace == "" {
		return ""
	}
	return subdomain + "." + workspace + ".localhost"
}

var invalidLabelRE = regexp.MustCompile(`[^a-z0-9-]+`)
var repeatedDashRE = regexp.MustCompile(`-+`)
var vitePortRE = regexp.MustCompile(`(?m)\bport\s*:\s*([0-9]+)\b`)
var netDialTimeout = net.DialTimeout

func sanitizeLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = invalidLabelRE.ReplaceAllString(value, "-")
	value = repeatedDashRE.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	return value
}

func parseVitePort(data []byte) int {
	matches := vitePortRE.FindSubmatch(data)
	if len(matches) != 2 {
		return 0
	}
	port, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return 0
	}
	return port
}

func discoverReachableLoopbackUpstream(port int) string {
	portStr := strconv.Itoa(port)
	candidates := []string{
		net.JoinHostPort("::1", portStr),
		net.JoinHostPort("127.0.0.1", portStr),
		net.JoinHostPort("localhost", portStr),
	}
	for _, candidate := range candidates {
		conn, err := netDialTimeout("tcp", candidate, 150*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return candidate
	}
	return net.JoinHostPort("localhost", portStr)
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func serverErrorLog(verbose bool) *log.Logger {
	if verbose {
		return nil
	}
	return log.New(io.Discard, "", 0)
}
