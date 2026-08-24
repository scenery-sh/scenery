package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

// TestAgentWatchdogRecoversDeadAgent proves the dev supervisor's watchdog
// issues a demand start when the agent stays unreachable, and stays quiet
// while the agent answers health.
func TestAgentWatchdogRecoversDeadAgent(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}

	var starts atomic.Int32
	started := make(chan struct{}, 1)
	policy := agentWatchdogPolicy{
		Interval:           time.Millisecond,
		RecoveryBackoff:    time.Millisecond,
		RecoveryBackoffMax: 4 * time.Millisecond,
		Start: func(localagent.Paths, localagent.StartOptions) error {
			starts.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			return nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)

	// Dead agent: no server on the socket. The watchdog must attempt a
	// recovery start after the failure threshold.
	stopped := startAgentAvailabilityWatchdog(ctx, client, paths, policy)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog never attempted recovery for a dead agent")
	}
	cancel()
	waitForAgentWatchdogStop(t, stopped)

	// Healthy agent: several successful checks complete without a recovery.
	paths = localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	var healthChecks atomic.Int32
	startCountingAgentHealthServer(t, paths.SocketPath, &healthChecks)
	healthyCtx, healthyCancel := context.WithCancel(context.Background())
	defer healthyCancel()
	starts.Store(0)
	healthyClient := localagent.NewClient(paths.SocketPath)
	t.Cleanup(healthyClient.CloseIdleConnections)
	healthyStopped := startAgentAvailabilityWatchdog(healthyCtx, healthyClient, paths, policy)
	waitForTestCondition(t, 2*time.Second, "healthy watchdog checks", func() bool {
		return healthChecks.Load() >= 4
	})
	if got := starts.Load(); got != 0 {
		t.Fatalf("watchdog attempted %d recoveries against a healthy agent", got)
	}
	healthyCancel()
	waitForAgentWatchdogStop(t, healthyStopped)
}

// TestAgentWatchdogStopsWhenHomeIsGoneOrNotConverging proves the two runaway
// guards: a deleted agent home stops the watchdog before any recovery (an
// orphaned runtime must not spawn agents that reap the real router owner),
// and non-converging recovery keeps retrying with exponential backoff
// instead of stopping — a permanent stop would disable the only unattended
// recovery path after a long external outage.
func TestAgentWatchdogStopsWhenHomeIsGoneOrNotConverging(t *testing.T) {
	t.Parallel()

	var starts atomic.Int32
	started := make(chan struct{}, 8)
	policy := agentWatchdogPolicy{
		Interval:           time.Millisecond,
		RecoveryBackoff:    time.Millisecond,
		RecoveryBackoffMax: 4 * time.Millisecond,
		Start: func(localagent.Paths, localagent.StartOptions) error {
			starts.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			return nil
		},
	}

	// Deleted home: no recovery at all.
	home := t.TempDir()
	paths := localagent.PathsForHome(home)
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := startAgentAvailabilityWatchdog(ctx, localagent.NewClient(paths.SocketPath), paths, policy)
	waitForAgentWatchdogStop(t, stopped)
	if got := starts.Load(); got != 0 {
		t.Fatalf("watchdog recovered %d times for a deleted agent home", got)
	}

	// Dead agent that never comes back: recovery keeps retrying with
	// backoff and never stops outright.
	paths = localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	starts.Store(0)
	capCtx, capCancel := context.WithCancel(context.Background())
	defer capCancel()
	capStopped := startAgentAvailabilityWatchdog(capCtx, localagent.NewClient(paths.SocketPath), paths, policy)
	t.Cleanup(func() { capCancel(); <-capStopped })
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("watchdog recovery attempts = %d, want at least 4", starts.Load())
		}
	}
	capCancel()
	waitForAgentWatchdogStop(t, capStopped)
}

// TestAgentWatchdogDisabled proves the watchdog respects SCENERY_AGENT_DISABLE
// and a nil client.
func TestAgentWatchdogDisabled(t *testing.T) {
	t.Setenv("SCENERY_AGENT_DISABLE", "1")
	var starts atomic.Int32
	policy := agentWatchdogPolicy{
		Interval: 10 * time.Millisecond,
		Start: func(localagent.Paths, localagent.StartOptions) error {
			starts.Add(1)
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paths := localagent.PathsForHome(t.TempDir())
	disabledStopped := startAgentAvailabilityWatchdog(ctx, localagent.NewClient(paths.SocketPath), paths, policy)
	nilStopped := startAgentAvailabilityWatchdog(ctx, nil, paths, policy)
	waitForAgentWatchdogStop(t, disabledStopped)
	waitForAgentWatchdogStop(t, nilStopped)
	if got := starts.Load(); got != 0 {
		t.Fatalf("disabled watchdog attempted %d recoveries", got)
	}
}

func startCountingAgentHealthServer(t *testing.T, socketPath string, checks *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/health" {
			http.NotFound(w, req)
			return
		}
		checks.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pid":42}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

func waitForAgentWatchdogStop(t *testing.T, stopped <-chan struct{}) {
	t.Helper()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent watchdog to stop")
	}
}
