package agent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHandlerTransport(t *testing.T, handlers map[string]http.Handler) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		handler, ok := handlers[req.URL.Host]
		if !ok {
			return nil, errors.New("unexpected test backend " + req.URL.Host)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		resp := recorder.Result()
		resp.Request = req
		return resp, nil
	})
}

func newInProcessTestServer(t *testing.T, transport http.RoundTripper) *Server {
	t.Helper()
	paths := PathsForHome(t.TempDir())
	if err := EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	registry, err := OpenRegistry(paths.RegistryPath, "127.0.0.1:9440", "http")
	if err != nil {
		t.Fatal(err)
	}
	registry.ownerVerifier = testOwnerVerifier
	registry.durable = false
	return &Server{
		paths:                paths,
		registry:             registry,
		routerAddr:           "127.0.0.1:9440",
		publicRouterAddr:     "127.0.0.1:9440",
		routerScheme:         "http",
		internalRouterScheme: "http",
		tcpTransport:         transport,
	}
}

func testProcessOwner() Owner {
	return Owner{
		PID:         os.Getpid(),
		StartedAt:   "test-process-start",
		CmdlineHash: "sha256:test-process",
		Exe:         "/test/scenery",
	}
}

func testOwnerVerifier(owner Owner) error {
	if owner.PID != os.Getpid() || owner.Exe == "/nonexistent/scenery" {
		return errors.New("test owner is stale")
	}
	return nil
}

func testRouteHost(t *testing.T, route string) string {
	t.Helper()
	parsed, err := url.Parse(route)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid route URL %q: %v", route, err)
	}
	host := parsed.Host
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return host
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
