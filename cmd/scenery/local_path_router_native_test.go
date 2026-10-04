//go:build scenery_local_router_integration

package main

import (
	"context"
	"fmt"
	"net/http"
	localagent "scenery.sh/internal/agent"
	"strings"
	"testing"
	"time"
)

// TestNativeLocalPathRouterDashboardFollowsAgentRestart proves the availability
// contract from the 2026-07-14 incident: after an agent restart moves the
// dashboard to a new loopback address, existing local path routers must stop
// proxying to the dead old backend and follow the current one, and a dead
// backend answers with a terse 502 instead of the default proxy error.
func TestNativeLocalPathRouterDashboardFollowsAgentRestart(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)
	_, backendA := startDashboardTestBackend(t, "dash-a")
	fake.setDashboard(backendA)

	retryClock := time.Unix(0, 0)
	retryWaited := make(chan struct{}, 1)
	dialRetry := localDialRetryPolicy{
		Budget:   400 * time.Millisecond,
		Interval: 50 * time.Millisecond,
		now:      func() time.Time { return retryClock },
		wait: func(context.Context, time.Duration) bool {
			retryClock = retryClock.Add(400 * time.Millisecond)
			select {
			case retryWaited <- struct{}{}:
			default:
			}
			return true
		},
	}

	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	session := localagent.Session{
		SessionID: "sess-1",
		AppRoot:   t.TempDir(),
		StateRoot: stateRoot,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := startLocalPathRouter(ctx, localPathRouterOptions{
		Session:          session,
		PortLease:        localagent.PortLease{Port: port, URL: fmt.Sprintf("http://localhost:%d", port)},
		EdgeToken:        "test-token",
		UpstreamAddr:     "127.0.0.1:1",
		DashboardBackend: backendA,
		DialRetry:        dialRetry,
		Agent:            localagent.NewClient(paths.SocketPath),
	})
	if err != nil {
		t.Fatalf("startLocalPathRouter: %v", err)
	}
	defer cleanup()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	storageURL := baseURL + localagent.PathModeRuntimeStoragePath + "?key=a.txt"
	statusCode, body := localPathRouterTestGet(t, storageURL)
	if statusCode != http.StatusOK || body != "dash-a:"+dashboardStoragePath+"?key=a.txt" {
		t.Fatalf("dashboard via backend A = %d %q", statusCode, body)
	}

	// Simulate a supervised agent restart: the dashboard moves to a new
	// loopback address and only agent health knows the new one.
	serverB, backendB := startDashboardTestBackend(t, "dash-b")
	fake.setDashboard(backendB)
	statusCode, body = localPathRouterTestGet(t, storageURL)
	if statusCode != http.StatusOK || body != "dash-b:"+dashboardStoragePath+"?key=a.txt" {
		t.Fatalf("dashboard after backend change = %d %q", statusCode, body)
	}

	// A dead current backend must answer with the terse scenery 502, not the
	// default httputil proxy error, once the bounded dial retry is exhausted.
	serverB.Close()
	statusCode, body = localPathRouterTestGet(t, storageURL)
	select {
	case <-retryWaited:
	default:
		t.Fatal("dead dashboard backend did not enter the bounded retry path")
	}
	if statusCode != http.StatusBadGateway {
		t.Fatalf("dead dashboard backend status = %d %q", statusCode, body)
	}
	if !strings.Contains(body, "scenery: dashboard backend") || strings.Contains(body, "proxy error") {
		t.Fatalf("dead dashboard backend body = %q", body)
	}
}
