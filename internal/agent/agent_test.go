package agent

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionIDUsesBranchAndRootHash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my-app")
	got := SessionID(root, "feature/Agent MVP")
	if !strings.HasPrefix(got, "feature-agent-mvp-") {
		t.Fatalf("SessionID prefix = %q", got)
	}
	if got2 := SessionID(root, "feature/Agent MVP"); got2 != got {
		t.Fatalf("SessionID not stable: %q then %q", got, got2)
	}
	if got2 := SessionID(filepath.Join(t.TempDir(), "my-app"), "feature/Agent MVP"); got2 == got {
		t.Fatalf("SessionID should include root hash, got duplicate %q", got)
	}
}

func TestRegistryUpsertWritesSessionManifest(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		Branch:    "feature/test",
		Status:    "running",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.RuntimeAppID != "demo--"+session.SessionID {
		t.Fatalf("runtime app id = %q", session.RuntimeAppID)
	}
	manifestPath := filepath.Join(root, ".onlava", "sessions", session.SessionID, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written at %s: %v", manifestPath, err)
	}
}

func TestServerRegistersAndRoutesSessionBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/hello" {
			t.Fatalf("backend path = %q, want /hello", req.URL.Path)
		}
		_, _ = io.WriteString(w, "backend ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer stopTestAgent(t, cancel, done)

	client := NewClient(server.paths.SocketPath)
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/router",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: backendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "api." + session.SessionID + ".onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "backend ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestUnixBackendRoute(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	socketPath := filepath.Join(t.TempDir(), "backend.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "unix ok")
	})}
	defer backend.Close()
	go func() { _ = backend.Serve(ln) }()

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer stopTestAgent(t, cancel, done)

	client := NewClient(server.paths.SocketPath)
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/unix",
		Backends: map[string]Backend{
			RouteAPI: {Network: "unix", Addr: socketPath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "api." + session.SessionID + ".onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "unix ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func waitForAgentPing(ctx context.Context, client *Client) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return lastErr
}

func stopTestAgent(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent server shutdown")
	}
}
