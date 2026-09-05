package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	edgelifecycle "scenery.sh/internal/edge"
)

// runHarnessEdgeStaticFrontendProbe exercises the published-file HTTP contract
// through a real managed Caddy. Certificate issuance and process-local
// settings are isolated; routes use the production renderer and publisher.
func runHarnessEdgeStaticFrontendProbe(ctx context.Context, root string) (proof map[string]any, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	paths, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	caddyBinary, err := resolveCaddyBinary(ctx, paths, false)
	if err != nil {
		return nil, fmt.Errorf("static frontend probe requires managed Caddy: %w", err)
	}
	source := filepath.Join(root, "static-source")
	for name, body := range map[string]string{
		"index.html":            "<html>app-platform</html>",
		"assets/app-abc123.js":  "console.log(1)",
		"models/scene.glb":      strings.Repeat("g", 4096),
		"nested/doc/index.html": "<html>nested</html>",
	} {
		if err := writeHarnessDesktopFile(filepath.Join(source, name), body, 0o600); err != nil {
			return nil, err
		}
	}
	release, err := edgelifecycle.PublishFrontendArtifact(edgelifecycle.PublishInput{
		ArtifactsRoot: filepath.Join(root, "static-artifacts"), AppID: "probe",
		Frontend: "platform", SourceDir: source, ReleaseID: "r1",
	})
	if err != nil {
		return nil, err
	}
	// Put a real sentinel outside the served release so traversal cannot pass
	// merely because there is nothing to disclose.
	if err := os.WriteFile(filepath.Join(release.CurrentPath, "..", "..", "secret"), []byte("private-sentinel"), 0o600); err != nil {
		return nil, err
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "agent:%s", r.URL.Path)
	}))
	defer upstream.Close()
	// Reserve both ports together so they cannot be selected as the same port.
	httpsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = httpsListener.Close() }()
	httpListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = httpListener.Close() }()
	addr := httpsListener.Addr().String()
	port := fmt.Sprint(httpsListener.Addr().(*net.TCPAddr).Port)
	config := edgelifecycle.CaddyConfig(edgelifecycle.CaddyConfigOptions{
		ListenAddr: addr, HTTPListenPort: fmt.Sprint(httpListener.Addr().(*net.TCPAddr).Port),
		Upstream: strings.TrimPrefix(upstream.URL, "http://"), Token: "probe-token",
		AdminSocket: filepath.Join(root, "static.sock"), StorageDir: filepath.Join(root, "static-storage"),
		PublicDomains: []edgelifecycle.PublicDomainSite{{Domain: "localhost", Frontends: []edgelifecycle.StaticFrontendRoute{{
			Name: "platform", Root: release.CurrentPath, BasePath: "/", OwnsRoot: true,
		}}}},
	})
	// No external ACME calls, admin socket, or trust-store installation during release proof.
	const publicTLS = "\ttls {\n\t\tissuer acme\n\t\tissuer internal\n\t}"
	if strings.Count(config, publicTLS) != 1 {
		return nil, fmt.Errorf("static probe expected one public TLS policy")
	}
	config = strings.Replace(config, publicTLS, "\ttls internal", 1)
	config = strings.Replace(config, "\tadmin unix//"+filepath.Join(root, "static.sock"), "\tadmin off", 1)
	config = strings.Replace(config, "{\n", "{\n\tskip_install_trust\n\tpersist_config off\n", 1)
	configPath := filepath.Join(root, "Caddyfile.static")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		return nil, err
	}
	logPath := filepath.Join(root, "static-caddy.log")
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.CommandContext(ctx, caddyBinary, "run", "--config", configPath, "--adapter", "caddyfile")
	cmd.Stdout, cmd.Stderr = log, log
	_ = httpsListener.Close()
	_ = httpListener.Close()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if err != nil {
			data, _ := os.ReadFile(logPath)
			if len(data) > 8192 {
				data = data[len(data)-8192:]
			}
			err = fmt.Errorf("%w\nCaddy log: %s", err, data)
		}
	}()
	// Trust only this disposable loopback connection without installing its CA.
	tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}
	transport := &http.Transport{TLSClientConfig: tlsConfig, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "https://" + addr
	request := func(method, path, rangeHeader string) (*http.Response, string, error) {
		req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
		if err != nil {
			return nil, "", err
		}
		req.Host = "localhost:" + port
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return resp, string(body), err
	}
	if err := waitForHarnessCondition(ctx, func() bool {
		resp, _, err := request("GET", "/", "")
		return err == nil && resp.StatusCode == http.StatusOK
	}); err != nil {
		return nil, fmt.Errorf("static Caddy readiness: %w", err)
	}
	type httpCheck struct {
		method, path, rangeHeader string
		status                    int
		body, cache               string
		length                    int64
		etag                      bool
	}
	checks := []httpCheck{
		{method: "GET", path: "/", status: 200, body: "<html>app-platform</html>", cache: "no-cache"},
		{method: "GET", path: "/assets/app-abc123.js", status: 200, body: "console.log(1)", cache: "immutable", etag: true},
		{method: "GET", path: "/deep/spa/route", status: 200, body: "<html>app-platform</html>"},
		{method: "HEAD", path: "/models/scene.glb", status: 200, length: 4096},
		{method: "GET", path: "/models/scene.glb", rangeHeader: "bytes=0-99", status: 206, length: 100, body: strings.Repeat("g", 100)},
		{method: "GET", path: "/assets/missing-xyz.js", status: 404},
		{method: "POST", path: "/", status: 405},
		{method: "GET", path: "/api/things", status: 200, body: "agent:/api/things"},
		{method: "GET", path: "/.hidden", status: 404},
	}
	for _, path := range []string{"/runtime", "/dashboard/x", "/__scenery/config", "/console", "/platform/api/x", "/platform/runtime", "/platform/__scenery/config"} {
		checks = append(checks, httpCheck{method: "GET", path: path, status: 404})
	}
	for _, check := range checks {
		resp, body, err := request(check.method, check.path, check.rangeHeader)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", check.method, check.path, err)
		}
		if resp.StatusCode != check.status || (check.body != "" && body != check.body) ||
			!strings.Contains(resp.Header.Get("Cache-Control"), check.cache) ||
			(check.length != 0 && resp.ContentLength != check.length) || (check.etag && resp.Header.Get("Etag") == "") {
			return nil, fmt.Errorf("%s %s: status=%d length=%d cache=%q etag=%q body=%q", check.method, check.path,
				resp.StatusCode, resp.ContentLength, resp.Header.Get("Cache-Control"), resp.Header.Get("Etag"), body)
		}
	}
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET /..%%2f..%%2fsecret HTTP/1.1\r\nHost: localhost:%s\r\nConnection: close\r\n\r\n", port); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(conn, 8192))
	if err != nil {
		return nil, err
	}
	status, _, _ := strings.Cut(string(raw), "\r\n")
	if !strings.HasPrefix(status, "HTTP/1.1 ") || strings.Contains(string(raw), "private-sentinel") ||
		(strings.Contains(status, " 200 ") && !strings.Contains(string(raw), "app-platform")) {
		return nil, fmt.Errorf("raw traversal escaped the static contract: %q", raw)
	}
	return map[string]any{"available": true, "http_checks": len(checks), "raw_traversal": "verified"}, nil
}
