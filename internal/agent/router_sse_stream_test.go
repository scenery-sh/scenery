package agent

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type streamingTestResponseWriter struct {
	header     http.Header
	body       *io.PipeWriter
	pending    bytes.Buffer
	status     chan int
	statusCode int
}

func (w *streamingTestResponseWriter) Header() http.Header {
	return w.header
}

func (w *streamingTestResponseWriter) WriteHeader(status int) {
	if w.statusCode != 0 {
		return
	}
	w.statusCode = status
	w.status <- status
}

func (w *streamingTestResponseWriter) Write(body []byte) (int, error) {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.pending.Write(body)
}

func (w *streamingTestResponseWriter) Flush() {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.pending.Len() == 0 {
		return
	}
	_, _ = w.pending.WriteTo(w.body)
}

// Repro: does the agent router proxy deliver SSE events incrementally,
// or does it buffer until the upstream closes?
func TestRouterStreamsSSEIncrementally(t *testing.T) {
	upstreamReader, upstreamWriter := io.Pipe()
	t.Cleanup(func() {
		_ = upstreamWriter.Close()
		_ = upstreamReader.Close()
	})
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}},
			Body:          upstreamReader,
			ContentLength: -1,
			Request:       req,
		}, nil
	})
	server := newInProcessTestServer(t, transport)
	session, err := server.registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "main",
		OwnerPID:  os.Getpid(),
		Owner:     testProcessOwner(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "stream.test"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := testRouteHost(t, session.RouteManifest.Routes[RouteAPI].URL)

	req := httptest.NewRequest(http.MethodGet, "http://router.test/v1/shape?live=true", nil)
	req.Host = host
	downstreamReader, downstreamWriter := io.Pipe()
	t.Cleanup(func() { _ = downstreamReader.Close() })
	response := &streamingTestResponseWriter{
		header: http.Header{},
		body:   downstreamWriter,
		status: make(chan int, 1),
	}
	handlerDone := make(chan struct{})
	go func() {
		server.routerMux().ServeHTTP(response, req)
		_ = downstreamWriter.Close()
		close(handlerDone)
	}()
	select {
	case status := <-response.status:
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("router did not start the SSE response")
	}

	type lineResult struct {
		line string
		err  error
	}
	reader := bufio.NewReader(downstreamReader)
	readLine := func() string {
		t.Helper()
		result := make(chan lineResult, 1)
		go func() {
			line, err := reader.ReadString('\n')
			result <- lineResult{line: line, err: err}
		}()
		select {
		case got := <-result:
			if got.err != nil {
				t.Fatalf("read SSE line: %v", got.err)
			}
			return got.line
		case <-time.After(time.Second):
			t.Fatal("SSE event was buffered while the upstream connection stayed open")
			return ""
		}
	}

	firstWrite := make(chan error, 1)
	go func() {
		_, err := io.WriteString(upstreamWriter, "data: first\n\n")
		firstWrite <- err
	}()
	if line := readLine(); line != "data: first\n" {
		t.Fatalf("first line = %q", line)
	}
	if line := readLine(); line != "\n" {
		t.Fatalf("first event terminator = %q", line)
	}
	if err := <-firstWrite; err != nil {
		t.Fatalf("write first event: %v", err)
	}
	select {
	case <-handlerDone:
		t.Fatal("router closed the SSE response before the upstream stream ended")
	default:
	}

	secondWrite := make(chan error, 1)
	go func() {
		_, err := io.WriteString(upstreamWriter, "data: second\n\n")
		_ = upstreamWriter.Close()
		secondWrite <- err
	}()
	if line := readLine(); line != "data: second\n" {
		t.Fatalf("second line = %q", line)
	}
	if line := readLine(); line != "\n" {
		t.Fatalf("second event terminator = %q", line)
	}
	if err := <-secondWrite; err != nil {
		t.Fatalf("write second event: %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("router did not finish after the upstream SSE stream ended")
	}
}

// Repro: a request while the session's backend is restarting (socket gone)
// must yield 503 + Retry-After so clients back off, not a hard 502.
func TestRouterReturnsRetryableServiceUnavailableWhileBackendRestarts(t *testing.T) {
	// Grab a port and close it so the dial is refused deterministically.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()

	server, err := NewServer(RunOptions{
		Home:       t.TempDir(),
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
	host := testRouteHost(t, session.RouteManifest.Routes[RouteAPI].URL)

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("missing Retry-After header on backend-unavailable response")
	}
}
