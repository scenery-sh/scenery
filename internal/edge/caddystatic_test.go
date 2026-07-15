package edge

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

// publishTestFrontend publishes a minimal production build and returns its
// `current` symlink path.
func publishTestFrontend(t *testing.T, artifactsRoot, appID, name string) string {
	t.Helper()
	source := t.TempDir()
	writePublishFixture(t, source, map[string]string{
		"index.html":            "<html>app-" + name + "</html>",
		"assets/app-abc123.js":  "console.log(1)",
		"models/scene.glb":      strings.Repeat("g", 4096),
		"nested/doc/index.html": "<html>nested</html>",
	})
	record, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: artifactsRoot, AppID: appID, Frontend: name, SourceDir: source, ReleaseID: "r1",
	})
	if err != nil {
		t.Fatalf("publish fixture: %v", err)
	}
	return record.CurrentPath
}

func TestCaddyConfigRendersStaticFrontendRoutes(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "microgrid-platform", "platform")
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain: "platform.onegraph.dev",
			Frontends: []StaticFrontendRoute{
				{Name: "platform", Root: current, OwnsRoot: true},
			},
		}},
		HTTPListenPort: "19080",
	})
	for _, want := range []string{
		"platform.onegraph.dev:19443 {",
		"@scenery_blocked path /runtime /runtime/* /dashboard /dashboard/* /console /console/* /__scenery /__scenery/*",
		"handle /api/* {",
		"redir /platform /platform/ 308",
		"handle_path /platform/* {",
		"root * " + current,
		"respond @fe_platform_method \"method not allowed\" 405",
		"encode zstd gzip",
		"rewrite @fe_platform_fallback /index.html",
		"header @fe_platform_immutable Cache-Control \"public, max-age=31536000, immutable\"",
		"header @fe_platform_revalidate Cache-Control \"no-cache\"",
		"file_server",
		"header_up X-Scenery-Public-Edge 1",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("static Caddy config missing %q:\n%s", want, config)
		}
	}
	// The root-owning frontend serves `/` statically: no catch-all proxy
	// after the static handles, but the /api handle still proxies.
	if got := strings.Count(config, "X-Scenery-Public-Edge 1"); got != 1 {
		t.Fatalf("expected exactly one public agent proxy (for /api), got %d:\n%s", got, config)
	}
}

func TestCaddyConfigStaticFrontendWithoutRootKeepsAgentCatchAll(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "app", "web")
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain:    "app.example.com",
			Frontends: []StaticFrontendRoute{{Name: "web", Root: current}},
		}},
	})
	if got := strings.Count(config, "X-Scenery-Public-Edge 1"); got != 2 {
		t.Fatalf("expected /api proxy plus catch-all proxy, got %d:\n%s", got, config)
	}
}

func TestCaddyConfigSkipsInvalidOrIncompleteStaticFrontends(t *testing.T) {
	t.Parallel()
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain: "app.example.com",
			Frontends: []StaticFrontendRoute{
				{Name: "web", Root: filepath.Join(t.TempDir(), "missing", "current")},
				{Name: "../escape", Root: "/tmp"},
				{Name: "api", Root: "/tmp"},
			},
		}},
	})
	if strings.Contains(config, "handle_path") || strings.Contains(config, "file_server") {
		t.Fatalf("unpublishable frontends must fall back to the agent proxy:\n%s", config)
	}
	if !strings.Contains(config, "app.example.com:19443 {") {
		t.Fatalf("domain site missing:\n%s", config)
	}
}

func TestCaddyConfigPublicDirectBindsPublicPorts(t *testing.T) {
	t.Parallel()
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:    "127.0.0.1:19443",
		Upstream:      "127.0.0.1:9440",
		AdminSocket:   "/tmp/scenery-caddy.sock",
		Token:         "secret-token",
		PublicDomains: []PublicDomainSite{{Domain: "app.example.com"}},
		PublicDirect:  true,
	})
	for _, want := range []string{
		"http_port 80",
		"https_port 443",
		"\napp.example.com {\n\tbind 0.0.0.0",
		"\nhttp://app.example.com {",
		"https://:19443 {",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("direct public config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "app.example.com:19443") {
		t.Fatalf("direct mode must not use the loopback forwarder port:\n%s", config)
	}
}

func TestPublicDomainSitesForDeployRegistryCarriesFrontends(t *testing.T) {
	t.Parallel()
	sites := publicDomainSitesForDeployRegistry(localagent.DeployRegistry{
		Targets: []localagent.DeployTarget{
			{Domain: "a.dev", Enabled: true, Frontends: []localagent.DeployTargetFrontend{
				{Name: "web", Path: "/x/current", Root: true},
			}},
			{Domain: "b.dev", Enabled: true},
		},
	})
	if len(sites) != 2 || len(sites[0].Frontends) != 1 || sites[0].Frontends[0].Name != "web" || !sites[0].Frontends[0].OwnsRoot {
		t.Fatalf("sites = %+v", sites)
	}
	if len(sites[1].Frontends) != 0 {
		t.Fatalf("target without publication metadata must stay proxy-only: %+v", sites[1])
	}
}

// findCaddyBinaryForTest locates an installed managed Caddy without
// downloading. Tests that need a live Caddy skip when none is present.
func findCaddyBinaryForTest(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".scenery", "toolchain", "artifacts", "caddy", "*", "*", "bin", "caddy"))
	if len(matches) == 0 {
		t.Skip("managed Caddy binary not installed")
	}
	return matches[len(matches)-1]
}

func TestCaddyValidateGeneratedStaticConfig(t *testing.T) {
	t.Parallel()
	caddyBin := findCaddyBinaryForTest(t)
	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "microgrid-platform", "platform")
	for _, direct := range []bool{false, true} {
		config := CaddyConfig(CaddyConfigOptions{
			ListenAddr:  "127.0.0.1:19443",
			Upstream:    "127.0.0.1:9440",
			AdminSocket: filepath.Join(t.TempDir(), "admin.sock"),
			Token:       "secret-token",
			PublicDomains: []PublicDomainSite{{
				Domain:    "platform.onegraph.dev",
				Frontends: []StaticFrontendRoute{{Name: "platform", Root: current, OwnsRoot: true}},
			}},
			StorageDir:     t.TempDir(),
			HTTPListenPort: "19080",
			PublicDirect:   direct,
		})
		configPath := filepath.Join(t.TempDir(), "Caddyfile")
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(caddyBin, "validate", "--config", configPath, "--adapter", "caddyfile").CombinedOutput()
		if err != nil {
			t.Fatalf("caddy validate (direct=%v): %v\n%s\n--- config ---\n%s", direct, err, out, config)
		}
	}
}

// TestCaddyStaticFrontendIntegration proves the static pipeline against a
// live managed Caddy on a loopback HTTP port: concrete files, SPA fallback,
// hashed-asset caching, HEAD, ranges, missing assets, method limits, blocked
// Scenery paths, and /api reaching the upstream agent stand-in.
func TestCaddyStaticFrontendIntegration(t *testing.T) {
	t.Parallel()
	caddyBin := findCaddyBinaryForTest(t)
	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "microgrid-platform", "platform")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "agent:%s", r.URL.Path)
	}))
	defer upstream.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	dir := t.TempDir()
	// Unix socket paths are limited to ~104 bytes on macOS; t.TempDir is
	// too deep for the admin socket.
	socketDir, err := os.MkdirTemp("", "scnedge")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	adminSocket := filepath.Join(socketDir, "admin.sock")
	proxy := publicAgentProxy(strings.TrimPrefix(upstream.URL, "http://"), "token")
	config := fmt.Sprintf(`{
	admin unix//%s
	auto_https off
}

http://127.0.0.1:%d {
	@scenery_blocked path /runtime /runtime/* /dashboard /dashboard/* /console /console/* /__scenery /__scenery/*
	handle @scenery_blocked {
		respond "not found" 404
	}
	handle /api/* {
%s	}
	redir /platform /platform/ 308
	handle_path /platform/* {
%s	}
	handle {
%s	}
}
`, adminSocket, port,
		indentBlock(proxy, 2),
		staticFrontendBody(StaticFrontendRoute{Name: "platform", Root: current}),
		staticFrontendBody(StaticFrontendRoute{Name: "platform", Root: current}))
	configPath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var caddyLog strings.Builder
	cmd := exec.Command(caddyBin, "run", "--config", configPath, "--adapter", "caddyfile")
	cmd.Stdout = &caddyLog
	cmd.Stderr = &caddyLog
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("caddy did not start listening:\n%s", caddyLog.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(method, path string, header http.Header) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key, values := range header {
			req.Header[key] = values
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	body := func(resp *http.Response) string {
		data, _ := io.ReadAll(resp.Body)
		return string(data)
	}

	if resp := get("GET", "/platform/", nil); resp.StatusCode != 200 || !strings.Contains(body(resp), "app-platform") {
		t.Fatalf("document: %d", resp.StatusCode)
	}
	if resp := get("GET", "/", nil); resp.StatusCode != 200 || !strings.Contains(body(resp), "app-platform") {
		t.Fatalf("root SPA document: %d", resp.StatusCode)
	}
	if resp := get("GET", "/platform/assets/app-abc123.js", nil); resp.StatusCode != 200 ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || resp.Header.Get("Etag") == "" {
		t.Fatalf("hashed asset: %d cache=%q etag=%q", resp.StatusCode, resp.Header.Get("Cache-Control"), resp.Header.Get("Etag"))
	}
	if resp := get("GET", "/platform/deep/spa/route", nil); resp.StatusCode != 200 || !strings.Contains(body(resp), "app-platform") {
		t.Fatalf("SPA fallback: %d", resp.StatusCode)
	}
	if resp := get("GET", "/platform/", nil); !strings.Contains(resp.Header.Get("Cache-Control"), "no-cache") {
		t.Fatalf("entry document must revalidate, cache=%q", resp.Header.Get("Cache-Control"))
	}
	if resp := get("HEAD", "/platform/models/scene.glb", nil); resp.StatusCode != 200 || resp.ContentLength != 4096 {
		t.Fatalf("HEAD: %d len=%d", resp.StatusCode, resp.ContentLength)
	}
	if resp := get("GET", "/platform/models/scene.glb", http.Header{"Range": []string{"bytes=0-99"}}); resp.StatusCode != 206 || resp.ContentLength != 100 {
		t.Fatalf("range: %d len=%d", resp.StatusCode, resp.ContentLength)
	}
	if resp := get("GET", "/platform/assets/missing-xyz.js", nil); resp.StatusCode != 404 {
		t.Fatalf("missing concrete asset must 404, got %d", resp.StatusCode)
	}
	if resp := get("POST", "/platform/", nil); resp.StatusCode != 405 {
		t.Fatalf("POST must 405, got %d", resp.StatusCode)
	}
	if resp := get("GET", "/platform", nil); resp.StatusCode != 308 {
		t.Fatalf("bare prefix must redirect, got %d", resp.StatusCode)
	}
	if resp := get("GET", "/api/things", nil); resp.StatusCode != 200 || body(resp) != "agent:/api/things" {
		t.Fatalf("API proxy: %d", resp.StatusCode)
	}
	for _, blocked := range []string{"/runtime", "/dashboard/x", "/__scenery/config", "/console"} {
		if resp := get("GET", blocked, nil); resp.StatusCode != 404 {
			t.Fatalf("blocked path %s must 404, got %d", blocked, resp.StatusCode)
		}
	}
	// The Go client normalizes dot segments, so exercise raw traversal
	// bytes over TCP; Caddy must not expose paths outside the release root.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "GET /platform/..%%2f..%%2fsecret HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
	raw, _ := io.ReadAll(conn)
	_ = conn.Close()
	status := strings.SplitN(string(raw), "\r\n", 2)[0]
	if strings.Contains(status, " 200 ") && !strings.Contains(string(raw), "app-platform") {
		t.Fatalf("raw traversal must not expose files outside the release root: %s", status)
	}
	if resp := get("GET", "/platform/.hidden", nil); resp.StatusCode == 200 {
		t.Fatal("dotfile must not be served")
	}
}
