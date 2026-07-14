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
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}

	oldInterval, oldBackoff, oldStart := agentWatchdogInterval, agentWatchdogRecoveryBackoff, agentWatchdogStartFunc
	t.Cleanup(func() {
		agentWatchdogInterval, agentWatchdogRecoveryBackoff, agentWatchdogStartFunc = oldInterval, oldBackoff, oldStart
	})
	agentWatchdogInterval = 20 * time.Millisecond
	agentWatchdogRecoveryBackoff = 50 * time.Millisecond
	var starts atomic.Int32
	started := make(chan struct{}, 8)
	agentWatchdogStartFunc = func(localagent.Paths, localagent.StartOptions) error {
		starts.Add(1)
		started <- struct{}{}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := localagent.NewClient(paths.SocketPath)

	// Dead agent: no server on the socket. The watchdog must attempt a
	// recovery start after the failure threshold.
	startAgentAvailabilityWatchdog(ctx, client)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog never attempted recovery for a dead agent")
	}
	cancel()

	// Healthy agent: no recovery attempts.
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err = localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	startFakeAgentHealthServer(t, paths.SocketPath, 42)
	healthyCtx, healthyCancel := context.WithCancel(context.Background())
	defer healthyCancel()
	starts.Store(0)
	startAgentAvailabilityWatchdog(healthyCtx, localagent.NewClient(paths.SocketPath))
	time.Sleep(200 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("watchdog attempted %d recoveries against a healthy agent", got)
	}
}

// TestAgentWatchdogStopsWhenHomeIsGoneOrNotConverging proves the two runaway
// guards: a deleted agent home stops the watchdog before any recovery (an
// orphaned runtime must not spawn agents that reap the real router owner),
// and recovery attempts are capped when the agent never comes back.
func TestAgentWatchdogStopsWhenHomeIsGoneOrNotConverging(t *testing.T) {
	oldInterval, oldBackoff, oldStart := agentWatchdogInterval, agentWatchdogRecoveryBackoff, agentWatchdogStartFunc
	t.Cleanup(func() {
		agentWatchdogInterval, agentWatchdogRecoveryBackoff, agentWatchdogStartFunc = oldInterval, oldBackoff, oldStart
	})
	agentWatchdogInterval = 10 * time.Millisecond
	agentWatchdogRecoveryBackoff = 10 * time.Millisecond
	var starts atomic.Int32
	agentWatchdogStartFunc = func(localagent.Paths, localagent.StartOptions) error {
		starts.Add(1)
		return nil
	}

	// Deleted home: no recovery at all.
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startAgentAvailabilityWatchdog(ctx, localagent.NewClient(paths.SocketPath))
	time.Sleep(200 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("watchdog recovered %d times for a deleted agent home", got)
	}
	cancel()

	// Dead agent that never comes back: recoveries stop at the cap.
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err = localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	starts.Store(0)
	capCtx, capCancel := context.WithCancel(context.Background())
	defer capCancel()
	startAgentAvailabilityWatchdog(capCtx, localagent.NewClient(paths.SocketPath))
	time.Sleep(500 * time.Millisecond)
	if got := starts.Load(); got != int32(agentWatchdogMaxRecoveries) {
		t.Fatalf("watchdog recovery attempts = %d, want %d", got, agentWatchdogMaxRecoveries)
	}
}

// TestAgentWatchdogDisabled proves the watchdog respects SCENERY_AGENT_DISABLE
// and a nil client.
func TestAgentWatchdogDisabled(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("SCENERY_AGENT_DISABLE", "1")
	oldInterval, oldStart := agentWatchdogInterval, agentWatchdogStartFunc
	t.Cleanup(func() { agentWatchdogInterval, agentWatchdogStartFunc = oldInterval, oldStart })
	agentWatchdogInterval = 10 * time.Millisecond
	var starts atomic.Int32
	agentWatchdogStartFunc = func(localagent.Paths, localagent.StartOptions) error {
		starts.Add(1)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	startAgentAvailabilityWatchdog(ctx, localagent.NewClient(paths.SocketPath))
	startAgentAvailabilityWatchdog(ctx, nil)
	time.Sleep(100 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("disabled watchdog attempted %d recoveries", got)
	}
}
