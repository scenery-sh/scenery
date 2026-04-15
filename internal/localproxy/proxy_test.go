package localproxy

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverWorkspace(t *testing.T) {
	got := DiscoverWorkspace("/tmp/Onlv Repo", "fallback")
	if got != "onlv-repo" {
		t.Fatalf("DiscoverWorkspace() = %q, want %q", got, "onlv-repo")
	}
	if got := DiscoverWorkspace("", "Onlv Next"); got != "onlv-next" {
		t.Fatalf("DiscoverWorkspace fallback = %q, want %q", got, "onlv-next")
	}
}

func TestNormalizeUpstream(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
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
		Workspace:         "onlv",
		APIUpstream:       "127.0.0.1:4000",
		DashboardUpstream: "127.0.0.1:9401",
		FrontendUpstream:  "127.0.0.1:5178",
		HTTPSPort:         9443,
	})
	if routes.APIURL != "https://api.onlv.localhost:9443" {
		t.Fatalf("APIURL = %q", routes.APIURL)
	}
	if routes.ConsoleURL != "https://console.onlv.localhost:9443" {
		t.Fatalf("ConsoleURL = %q", routes.ConsoleURL)
	}
	if routes.MCPBaseURL != "https://mcp.onlv.localhost:9443" {
		t.Fatalf("MCPBaseURL = %q", routes.MCPBaseURL)
	}
	if routes.FrontendURL != "https://pulse.onlv.localhost:9443" {
		t.Fatalf("FrontendURL = %q", routes.FrontendURL)
	}
	if got := ConsoleAppURL(routes, "onlvnext-o5o2"); got != "https://console.onlv.localhost:9443/onlvnext-o5o2" {
		t.Fatalf("ConsoleAppURL = %q", got)
	}
	if got := MCPSSEURL(routes, "onlvnext-o5o2"); got != "https://mcp.onlv.localhost:9443/sse?appID=onlvnext-o5o2" {
		t.Fatalf("MCPSSEURL = %q", got)
	}
}

func TestRoutesForExplicitHosts(t *testing.T) {
	routes := routesFor(Config{
		APIHost:           "api.custom.localhost",
		ConsoleHost:       "console.custom.localhost",
		MCPHost:           "mcp.custom.localhost",
		FrontendHost:      "pulse.custom.localhost",
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
	if routes.FrontendURL != "https://pulse.custom.localhost:9443" {
		t.Fatalf("FrontendURL = %q", routes.FrontendURL)
	}
}

func TestCaddyfileIncludesExpectedHosts(t *testing.T) {
	cfg := Config{
		Workspace:         "onlv",
		APIUpstream:       "127.0.0.1:4000",
		DashboardUpstream: "127.0.0.1:9401",
		FrontendUpstream:  "127.0.0.1:5178",
		HTTPPort:          9080,
		HTTPSPort:         9443,
		SkipInstallTrust:  true,
	}
	got := caddyfile(cfg)
	for _, want := range []string{
		"local_certs",
		"skip_install_trust",
		"http_port 9080",
		"https_port 9443",
		"api.onlv.localhost",
		"console.onlv.localhost",
		"mcp.onlv.localhost",
		"pulse.onlv.localhost",
		"@pulse_config path /__pulse/config",
		"reverse_proxy @pulse_config 127.0.0.1:4000",
		"header_up Host {upstream_hostport}",
	} {
		if !contains(got, want) {
			t.Fatalf("caddyfile missing %q in:\n%s", want, got)
		}
	}
}

func TestCaddyfileSuppressesLogsWhenNotVerbose(t *testing.T) {
	quiet := caddyfile(Config{
		Workspace:   "onlv",
		APIUpstream: "127.0.0.1:4000",
	})
	for _, want := range []string{
		"log default",
		"output stderr",
		"level PANIC",
	} {
		if !contains(quiet, want) {
			t.Fatalf("quiet caddyfile missing %q:\n%s", want, quiet)
		}
	}

	verbose := caddyfile(Config{
		Workspace:   "onlv",
		APIUpstream: "127.0.0.1:4000",
		Verbose:     true,
	})
	for _, unwanted := range []string{"log default", "level PANIC"} {
		if contains(verbose, unwanted) {
			t.Fatalf("verbose caddyfile should not include %q:\n%s", unwanted, verbose)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "api.onlv.localhost", want: "api.onlv.localhost"},
		{input: "HTTPS://API.ONLV.LOCALHOST/path", want: "api.onlv.localhost"},
		{input: "api.onlv.localhost:443", want: "api.onlv.localhost"},
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
	vitePath := filepath.Join(root, "apps", "pulse", "vite.config.ts")
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

func contains(s, want string) bool {
	return strings.Contains(s, want)
}
