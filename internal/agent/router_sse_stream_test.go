package agent

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Repro: does the agent router proxy deliver SSE events incrementally,
// or does it buffer until the upstream closes?
func TestRouterStreamsSSEIncrementally(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())

	release := make(chan struct{})
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // hold the stream open like a live event feed
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer stream.Close()
	defer close(release)
	streamAddr := strings.TrimPrefix(stream.URL, "http://")

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
		Branch:    "main",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: streamAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := testRouteHost(t, session.Routes[RouteAPI])

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/v1/shape?live=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	type lineResult struct {
		line string
		err  error
	}
	lines := make(chan lineResult, 1)
	go func() {
		r := bufio.NewReader(resp.Body)
		line, err := r.ReadString('\n')
		lines <- lineResult{line: line, err: err}
	}()
	select {
	case got := <-lines:
		if got.err != nil {
			t.Fatalf("read error before first event: %v", got.err)
		}
		if !strings.Contains(got.line, "first") {
			t.Fatalf("first line = %q", got.line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE event was buffered: nothing received within 3s while upstream connection stayed open")
	}
}

// Repro: a request while the session's backend is restarting (socket gone)
// must yield 503 + Retry-After so clients back off, not a hard 502.
func TestRouterReturnsRetryableServiceUnavailableWhileBackendRestarts(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())

	// Grab a port and close it so the dial is refused deterministically.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()

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
		Branch:    "main",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: deadAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := testRouteHost(t, session.Routes[RouteAPI])

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("missing Retry-After header on backend-unavailable response")
	}
}
