package main

import (
	"context"
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
	started := make(chan struct{}, 8)
	policy := agentWatchdogPolicy{
		Interval:        20 * time.Millisecond,
		RecoveryBackoff: 50 * time.Millisecond,
		Start: func(localagent.Paths, localagent.StartOptions) error {
			starts.Add(1)
			started <- struct{}{}
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
	<-stopped

	// Healthy agent: no recovery attempts.
	paths = localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	startFakeAgentHealthServer(t, paths.SocketPath, 42)
	healthyCtx, healthyCancel := context.WithCancel(context.Background())
	defer healthyCancel()
	starts.Store(0)
	healthyStopped := startAgentAvailabilityWatchdog(healthyCtx, localagent.NewClient(paths.SocketPath), paths, policy)
	time.Sleep(200 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("watchdog attempted %d recoveries against a healthy agent", got)
	}
	healthyCancel()
	<-healthyStopped
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
	policy := agentWatchdogPolicy{
		Interval:        10 * time.Millisecond,
		RecoveryBackoff: 10 * time.Millisecond,
		Start: func(localagent.Paths, localagent.StartOptions) error {
			starts.Add(1)
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
	time.Sleep(200 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("watchdog recovered %d times for a deleted agent home", got)
	}
	cancel()
	<-stopped

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
	// Positive assertion: poll instead of sleeping a fixed 500ms. The retries
	// are the thing under test, so finish as soon as enough have landed and
	// still tolerate a loaded machine. The watchdog interval is deliberately
	// left alone: shortening it makes the negative assertions above flaky
	// under -race, where setup itself takes several intervals.
	waitForTestCondition(t, 5*time.Second, "watchdog recovery to keep retrying", func() bool {
		return starts.Load() >= 4
	})
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
	time.Sleep(100 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("disabled watchdog attempted %d recoveries", got)
	}
	cancel()
	<-disabledStopped
	<-nilStopped
}
