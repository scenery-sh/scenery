package agent

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"
)

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
