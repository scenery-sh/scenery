package localproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiscoverWorkspace(t *testing.T) {
	got := DiscoverWorkspace("/tmp/Acme Repo", "fallback")
	if got != "acme-repo" {
		t.Fatalf("DiscoverWorkspace() = %q, want %q", got, "acme-repo")
	}
	if got := DiscoverWorkspace("", "Acme Next"); got != "acme-next" {
		t.Fatalf("DiscoverWorkspace fallback = %q, want %q", got, "acme-next")
	}
}

func TestProxyAndTrustDefaultsAreOptIn(t *testing.T) {
	t.Setenv("ONLAVA_LOCAL_PROXY", "")
	t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", "")
	if Enabled() {
		t.Fatal("local proxy enabled by default")
	}
	if !SkipInstallTrust() {
		t.Fatal("trust installation should be skipped by default")
	}
	t.Setenv("ONLAVA_LOCAL_PROXY", "1")
	t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", "0")
	if !Enabled() {
		t.Fatal("local proxy not enabled by explicit env")
	}
	if SkipInstallTrust() {
		t.Fatal("trust installation should be allowed by explicit env")
	}
}

func TestEnvironmentParsing(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off"} {
		t.Setenv("ONLAVA_LOCAL_PROXY", value)
		if Enabled() {
			t.Fatalf("Enabled() = true for %q", value)
		}
		t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", value)
		if SkipInstallTrust() {
			t.Fatalf("SkipInstallTrust() = true for %q", value)
		}
	}
	for _, value := range []string{"1", "true", "yes", "on"} {
		t.Setenv("ONLAVA_LOCAL_PROXY", value)
		if !Enabled() {
			t.Fatalf("Enabled() = false for %q", value)
		}
		t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", value)
		if !SkipInstallTrust() {
			t.Fatalf("SkipInstallTrust() = false for %q", value)
		}
	}
	t.Setenv("ONLAVA_LOCAL_PROXY_HTTP_PORT", "9080")
	t.Setenv("ONLAVA_LOCAL_PROXY_HTTPS_PORT", "9443")
	if HTTPPort() != 9080 {
		t.Fatalf("HTTPPort() = %d", HTTPPort())
	}
	if HTTPSPort() != 9443 {
		t.Fatalf("HTTPSPort() = %d", HTTPSPort())
	}
	t.Setenv("ONLAVA_FRONTEND_ADDR", "http://0.0.0.0:5178")
	if got := FrontendOverride(); got != "127.0.0.1:5178" {
		t.Fatalf("FrontendOverride() = %q", got)
	}
	t.Setenv("ONLAVA_DISABLE_FRONTEND_PROXY", "1")
	if got := DiscoverFrontendUpstream(t.TempDir()); got != "" {
		t.Fatalf("DiscoverFrontendUpstream disabled = %q", got)
	}
}

func TestNormalizeUpstream(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "0.0.0.0:4000", want: "127.0.0.1:4000"},
		{input: ":4000", want: "127.0.0.1:4000"},
		{input: "127.0.0.1:5178", want: "127.0.0.1:5178"},
		{input: "http://127.0.0.1:5178", want: "127.0.0.1:5178"},
	}
	for _, tt := range tests {
		if got := normalizeUpstream(tt.input); got != tt.want {
			t.Fatalf("normalizeUpstream(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRoutesFor(t *testing.T) {
	routes := routesFor(Config{
		Workspace:         "acme",
		APIUpstream:       "127.0.0.1:4000",
		DashboardUpstream: "127.0.0.1:9401",
		FrontendUpstream:  "127.0.0.1:5178",
		HTTPSPort:         9443,
	})
	if routes.APIURL != "https://api.acme.localhost:9443" {
		t.Fatalf("APIURL = %q", routes.APIURL)
	}
	if routes.ConsoleURL != "https://console.acme.localhost:9443" {
		t.Fatalf("ConsoleURL = %q", routes.ConsoleURL)
	}
	if routes.MCPBaseURL != "https://mcp.acme.localhost:9443" {
		t.Fatalf("MCPBaseURL = %q", routes.MCPBaseURL)
	}
	if routes.FrontendURL != "https://onlava.acme.localhost:9443" {
		t.Fatalf("FrontendURL = %q", routes.FrontendURL)
	}
	if got := ConsoleAppURL(routes, "demoapp-dev"); got != "https://console.acme.localhost:9443/demoapp-dev" {
		t.Fatalf("ConsoleAppURL = %q", got)
	}
	if got := MCPSSEURL(routes, "demoapp-dev"); got != "https://mcp.acme.localhost:9443/sse?appID=demoapp-dev" {
		t.Fatalf("MCPSSEURL = %q", got)
	}
}

func TestRoutesForExplicitHosts(t *testing.T) {
	routes := routesFor(Config{
		APIHost:           "api.custom.localhost",
		ConsoleHost:       "console.custom.localhost",
		MCPHost:           "mcp.custom.localhost",
		FrontendHost:      "onlava.custom.localhost",
		APIUpstream:       "127.0.0.1:4000",
		DashboardUpstream: "127.0.0.1:9401",
		FrontendUpstream:  "127.0.0.1:5178",
		HTTPSPort:         9443,
	})
	if routes.APIURL != "https://api.custom.localhost:9443" {
		t.Fatalf("APIURL = %q", routes.APIURL)
	}
	if routes.ConsoleURL != "https://console.custom.localhost:9443" {
		t.Fatalf("ConsoleURL = %q", routes.ConsoleURL)
	}
	if routes.MCPBaseURL != "https://mcp.custom.localhost:9443" {
		t.Fatalf("MCPBaseURL = %q", routes.MCPBaseURL)
	}
	if routes.FrontendURL != "https://onlava.custom.localhost:9443" {
		t.Fatalf("FrontendURL = %q", routes.FrontendURL)
	}
}

func TestRouteTableIncludesExpectedHosts(t *testing.T) {
	table, err := proxyRoutes(Config{
		Workspace:         "acme",
		APIUpstream:       "127.0.0.1:4000",
		DashboardUpstream: "127.0.0.1:9401",
		FrontendUpstream:  "127.0.0.1:5178",
	})
	if err != nil {
		t.Fatalf("proxyRoutes() error = %v", err)
	}
	want := []proxyRoute{
		{host: "api.acme.localhost", upstream: "127.0.0.1:4000"},
		{host: "console.acme.localhost", upstream: "127.0.0.1:9401"},
		{host: "mcp.acme.localhost", upstream: "127.0.0.1:9401"},
		{host: "onlava.acme.localhost", path: "/__onlava/config", upstream: "127.0.0.1:4000"},
		{host: "onlava.acme.localhost", upstream: "127.0.0.1:5178", rewriteHost: true},
	}
	if len(table) != len(want) {
		t.Fatalf("route count = %d, want %d", len(table), len(want))
	}
	for i := range want {
		got := table[i]
		if got.host != want[i].host || got.path != want[i].path || got.upstream != want[i].upstream || got.rewriteHost != want[i].rewriteHost {
			t.Fatalf("route %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestCertificateSubjects(t *testing.T) {
	subjects := routeSubjects(Config{
		Workspace:         "acme",
		APIUpstream:       "127.0.0.1:4000",
		DashboardUpstream: "127.0.0.1:9401",
		FrontendUpstream:  "127.0.0.1:5178",
	})
	want := []string{
		"api.acme.localhost",
		"console.acme.localhost",
		"mcp.acme.localhost",
		"onlava.acme.localhost",
	}
	if strings.Join(subjects, ",") != strings.Join(want, ",") {
		t.Fatalf("routeSubjects() = %#v, want %#v", subjects, want)
	}
}

func TestStartRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "missing api upstream",
			cfg:  Config{Workspace: "acme"},
			want: "local proxy requires an API upstream",
		},
		{
			name: "missing api host",
			cfg:  Config{APIUpstream: "127.0.0.1:4000"},
			want: "local proxy requires an API host or workspace label",
		},
		{
			name: "missing dashboard hosts",
			cfg:  Config{APIHost: "api.custom.localhost", APIUpstream: "127.0.0.1:4000", DashboardUpstream: "127.0.0.1:9401"},
			want: "local proxy requires console and mcp hosts when dashboard routing is enabled",
		},
		{
			name: "missing frontend host",
			cfg:  Config{APIHost: "api.custom.localhost", APIUpstream: "127.0.0.1:4000", FrontendUpstream: "127.0.0.1:5178"},
			want: "local proxy requires a frontend host when frontend routing is enabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Start(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Start() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "api.acme.localhost", want: "api.acme.localhost"},
		{input: "HTTPS://API.ACME.LOCALHOST/path", want: "api.acme.localhost"},
		{input: "api.acme.localhost:443", want: "api.acme.localhost"},
	}
	for _, tt := range tests {
		if got := normalizeHost(tt.input); got != tt.want {
			t.Fatalf("normalizeHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDiscoverFrontendUpstreamFromWorkspace(t *testing.T) {
	oldDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("unreachable")
	}
	t.Cleanup(func() { netDialTimeout = oldDial })

	root := t.TempDir()
	vitePath := filepath.Join(root, "apps", "onlava", "vite.config.ts")
	if err := os.MkdirAll(filepath.Dir(vitePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vitePath, []byte("export default { server: { port: 5178 } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverFrontendUpstream(root); got != "localhost:5178" {
		t.Fatalf("DiscoverFrontendUpstream() = %q, want %q", got, "localhost:5178")
	}
}

func TestDiscoverReachableLoopbackUpstream(t *testing.T) {
	oldDial := netDialTimeout
	t.Cleanup(func() { netDialTimeout = oldDial })

	tests := []struct {
		name      string
		reachable map[string]bool
		want      string
	}{
		{
			name:      "prefers IPv6 loopback when reachable",
			reachable: map[string]bool{"[::1]:5178": true},
			want:      "[::1]:5178",
		},
		{
			name:      "falls back to IPv4 loopback",
			reachable: map[string]bool{"127.0.0.1:5178": true},
			want:      "127.0.0.1:5178",
		},
		{
			name:      "uses localhost fallback when no listener is reachable",
			reachable: map[string]bool{},
			want:      "localhost:5178",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
				if network != "tcp" {
					t.Fatalf("network = %q, want tcp", network)
				}
				if timeout <= 0 {
					t.Fatalf("timeout = %v, want positive", timeout)
				}
				if tt.reachable[address] {
					conn, peer := net.Pipe()
					_ = peer.Close()
					return conn, nil
				}
				return nil, errors.New("unreachable")
			}

			if got := discoverReachableLoopbackUpstream(5178); got != tt.want {
				t.Fatalf("discoverReachableLoopbackUpstream() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProxyRoutesAndRedirects(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)

	api := newEchoServer(t, "api")
	dashboard := newEchoServer(t, "dashboard")
	frontend := newEchoServer(t, "frontend")
	defer api.Close()
	defer dashboard.Close()
	defer frontend.Close()

	httpPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	proxy, err := Start(Config{
		Workspace:         "acme",
		APIUpstream:       api.URL,
		DashboardUpstream: dashboard.URL,
		FrontendUpstream:  frontend.URL,
		HTTPPort:          httpPort,
		HTTPSPort:         httpsPort,
		SkipInstallTrust:  true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proxy.Close()

	client := newProxyClient(t, cacheDir)
	apiEcho := getEcho(t, client, fmt.Sprintf("https://api.acme.localhost:%d/v1?x=1", httpsPort))
	if apiEcho.Server != "api" || apiEcho.Host != fmt.Sprintf("api.acme.localhost:%d", httpsPort) || apiEcho.Path != "/v1" || apiEcho.RawQuery != "x=1" {
		t.Fatalf("api echo = %+v", apiEcho)
	}
	if apiEcho.ForwardedHost != fmt.Sprintf("api.acme.localhost:%d", httpsPort) || apiEcho.ForwardedProto != "https" {
		t.Fatalf("api forwarded headers = %+v", apiEcho)
	}

	consoleEcho := getEcho(t, client, fmt.Sprintf("https://console.acme.localhost:%d/dashboard", httpsPort))
	if consoleEcho.Server != "dashboard" || consoleEcho.Host != fmt.Sprintf("console.acme.localhost:%d", httpsPort) {
		t.Fatalf("console echo = %+v", consoleEcho)
	}
	mcpEcho := getEcho(t, client, fmt.Sprintf("https://mcp.acme.localhost:%d/sse", httpsPort))
	if mcpEcho.Server != "dashboard" || mcpEcho.Host != fmt.Sprintf("mcp.acme.localhost:%d", httpsPort) {
		t.Fatalf("mcp echo = %+v", mcpEcho)
	}

	configEcho := getEcho(t, client, fmt.Sprintf("https://onlava.acme.localhost:%d/__onlava/config", httpsPort))
	if configEcho.Server != "api" || configEcho.Host != fmt.Sprintf("onlava.acme.localhost:%d", httpsPort) {
		t.Fatalf("frontend config echo = %+v", configEcho)
	}
	frontendEcho := getEcho(t, client, fmt.Sprintf("https://onlava.acme.localhost:%d/app", httpsPort))
	if frontendEcho.Server != "frontend" || frontendEcho.Host != normalizeUpstream(frontend.URL) {
		t.Fatalf("frontend echo = %+v", frontendEcho)
	}
	if frontendEcho.ForwardedHost != fmt.Sprintf("onlava.acme.localhost:%d", httpsPort) {
		t.Fatalf("frontend forwarded host = %q", frontendEcho.ForwardedHost)
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://api.acme.localhost:%d/nope", httpsPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = fmt.Sprintf("unknown.acme.localhost:%d", httpsPort)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unknown host request error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown host status = %d", resp.StatusCode)
	}

	resp, err = client.Get(fmt.Sprintf("http://api.acme.localhost:%d/v1?x=1", httpPort))
	if err != nil {
		t.Fatalf("http redirect request error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("redirect status = %d", resp.StatusCode)
	}
	wantLocation := fmt.Sprintf("https://api.acme.localhost:%d/v1?x=1", httpsPort)
	if got := resp.Header.Get("Location"); got != wantLocation {
		t.Fatalf("redirect Location = %q, want %q", got, wantLocation)
	}
}

func TestProxyServesHTTP2(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)

	api := newEchoServer(t, "api")
	defer api.Close()

	httpPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	proxy, err := Start(Config{
		Workspace:        "acme",
		APIUpstream:      api.URL,
		HTTPPort:         httpPort,
		HTTPSPort:        httpsPort,
		SkipInstallTrust: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proxy.Close()

	client := newProxyClient(t, cacheDir)
	resp, err := client.Get(fmt.Sprintf("https://api.acme.localhost:%d/v1", httpsPort))
	if err != nil {
		t.Fatalf("HTTP/2 proxy request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("proxy response protocol = %s, want HTTP/2", resp.Proto)
	}
}

func TestCloseIsIdempotentAndReleasesPorts(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	api := newEchoServer(t, "api")
	defer api.Close()

	httpPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	proxy, err := Start(Config{
		Workspace:        "acme",
		APIUpstream:      api.URL,
		HTTPPort:         httpPort,
		HTTPSPort:        httpsPort,
		SkipInstallTrust: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	for _, port := range []int{httpPort, httpsPort} {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		_ = ln.Close()
	}
}

func TestStartContinuesWhenHTTPRedirectPortUnavailable(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	api := newEchoServer(t, "api")
	defer api.Close()

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on blocker port: %v", err)
	}
	defer blocker.Close()
	httpPort := blocker.Addr().(*net.TCPAddr).Port
	httpsPort := freeTCPPort(t)

	proxy, err := Start(Config{
		Workspace:        "acme",
		APIUpstream:      api.URL,
		HTTPPort:         httpPort,
		HTTPSPort:        httpsPort,
		SkipInstallTrust: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proxy.Close()

	client := newProxyClient(t, cacheDir)
	echo := getEcho(t, client, fmt.Sprintf("https://api.acme.localhost:%d/v1", httpsPort))
	if echo.Server != "api" {
		t.Fatalf("echo server = %q, want api", echo.Server)
	}
}

func TestStartInstallsTrustWhenNotSkipped(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	api := newEchoServer(t, "api")
	defer api.Close()

	oldTrusted := localCATrusted
	oldInstaller := installLocalCATrust
	localCATrusted = func(certPath string) (bool, error) {
		if _, err := os.Stat(certPath); err != nil {
			t.Fatalf("trust cert path does not exist: %v", err)
		}
		return false, nil
	}
	var calledPath string
	installLocalCATrust = func(certPath string) error {
		calledPath = certPath
		if _, err := os.Stat(certPath); err != nil {
			t.Fatalf("trust cert path does not exist: %v", err)
		}
		return nil
	}
	t.Cleanup(func() {
		localCATrusted = oldTrusted
		installLocalCATrust = oldInstaller
	})

	proxy, err := Start(Config{
		Workspace:        "acme",
		APIUpstream:      api.URL,
		HTTPPort:         freeTCPPort(t),
		HTTPSPort:        freeTCPPort(t),
		SkipInstallTrust: false,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proxy.Close()
	if calledPath == "" {
		t.Fatal("trust installer was not called")
	}
}

func TestStartSkipsTrustInstallerWhenAlreadyTrusted(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)
	api := newEchoServer(t, "api")
	defer api.Close()

	oldTrusted := localCATrusted
	oldInstaller := installLocalCATrust
	var checkedPath string
	localCATrusted = func(certPath string) (bool, error) {
		checkedPath = certPath
		if _, err := os.Stat(certPath); err != nil {
			t.Fatalf("trust cert path does not exist: %v", err)
		}
		return true, nil
	}
	installLocalCATrust = func(certPath string) error {
		t.Fatalf("trust installer should not be called for trusted cert %s", certPath)
		return nil
	}
	t.Cleanup(func() {
		localCATrusted = oldTrusted
		installLocalCATrust = oldInstaller
	})

	proxy, err := Start(Config{
		Workspace:        "acme",
		APIUpstream:      api.URL,
		HTTPPort:         freeTCPPort(t),
		HTTPSPort:        freeTCPPort(t),
		SkipInstallTrust: false,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proxy.Close()
	if checkedPath == "" {
		t.Fatal("trust status was not checked")
	}
}

func TestLocalCertificatesIncludeExpectedSANsAndReuseCA(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheDir)

	first, err := prepareLocalCertificates([]string{"api.acme.localhost", "console.acme.localhost"})
	if err != nil {
		t.Fatalf("prepareLocalCertificates() error = %v", err)
	}
	if err := first.Leaf.Leaf.VerifyHostname("api.acme.localhost"); err != nil {
		t.Fatalf("leaf does not cover api host: %v", err)
	}
	if err := first.Leaf.Leaf.VerifyHostname("console.acme.localhost"); err != nil {
		t.Fatalf("leaf does not cover console host: %v", err)
	}
	caSerial := first.CACert.SerialNumber.String()
	leafSerial := first.Leaf.Leaf.SerialNumber.String()

	second, err := prepareLocalCertificates([]string{"console.acme.localhost", "api.acme.localhost"})
	if err != nil {
		t.Fatalf("second prepareLocalCertificates() error = %v", err)
	}
	if second.CACert.SerialNumber.String() != caSerial {
		t.Fatalf("CA serial changed on reuse")
	}
	if second.Leaf.Leaf.SerialNumber.String() != leafSerial {
		t.Fatalf("leaf serial changed despite same subjects")
	}

	third, err := prepareLocalCertificates([]string{"api.acme.localhost", "onlava.acme.localhost"})
	if err != nil {
		t.Fatalf("third prepareLocalCertificates() error = %v", err)
	}
	if third.CACert.SerialNumber.String() != caSerial {
		t.Fatalf("CA serial changed when regenerating leaf")
	}
	if third.Leaf.Leaf.SerialNumber.String() == leafSerial {
		t.Fatalf("leaf serial did not change after SAN set changed")
	}
	if err := third.Leaf.Leaf.VerifyHostname("onlava.acme.localhost"); err != nil {
		t.Fatalf("regenerated leaf does not cover frontend host: %v", err)
	}

	if runtime.GOOS != "windows" {
		dir, err := localProxyCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		if mode := fileMode(t, dir); mode != 0o700 {
			t.Fatalf("cache dir mode = %#o, want 0700", mode)
		}
		for _, name := range []string{localProxyCACertFile, localProxyCAKeyFile, localProxyLeafCertFile, localProxyLeafKeyFile} {
			if mode := fileMode(t, filepath.Join(dir, name)); mode != 0o600 {
				t.Fatalf("%s mode = %#o, want 0600", name, mode)
			}
		}
	}
}

type requestEcho struct {
	Server         string `json:"server"`
	Host           string `json:"host"`
	Path           string `json:"path"`
	RawQuery       string `json:"raw_query"`
	ForwardedHost  string `json:"forwarded_host"`
	ForwardedProto string `json:"forwarded_proto"`
}

func newEchoServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(requestEcho{
			Server:         name,
			Host:           r.Host,
			Path:           r.URL.Path,
			RawQuery:       r.URL.RawQuery,
			ForwardedHost:  r.Header.Get("X-Forwarded-Host"),
			ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
		})
	}))
}

func newProxyClient(t *testing.T, cacheDir string) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(cacheDir, "onlava", "localproxy", localProxyCACertFile))
	if err != nil {
		t.Fatalf("read local CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("append local CA")
	}
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			RootCAs: roots,
		},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func getEcho(t *testing.T, client *http.Client, url string) requestEcho {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status = %d: %s", url, resp.StatusCode, body)
	}
	var echo requestEcho
	if err := json.NewDecoder(resp.Body).Decode(&echo); err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	return echo
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on random port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
