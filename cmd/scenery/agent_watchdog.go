package main

import (
	"context"
	"os"
	"time"

	localagent "scenery.sh/internal/agent"
)

var (
	agentWatchdogInterval           = 2 * time.Second
	agentWatchdogFailThreshold      = 2
	agentWatchdogRecoveryBackoff    = 10 * time.Second
	agentWatchdogRecoveryBackoffMax = 5 * time.Minute
	agentWatchdogStartFunc          = localagent.StartProcess
	agentWatchdogLog                = newComponentFailureLog("scenery-agent-watchdog", 30*time.Second, time.Now)
)

// startAgentAvailabilityWatchdog keeps the local agent alive from inside the
// long-running dev supervisor. launchd can pend the supervised gui
// LaunchAgent's KeepAlive respawn indefinitely ("pended nondemand spawn"), so
// a crashed agent is not guaranteed to come back on its own; every live
// `scenery up` supervisor therefore recovers a dead agent with an explicit
// demand start — a launchd kickstart when the supervisor plist owns the
// socket, an ordinary lock-protected spawn otherwise. Concurrent watchdogs
// are safe: kickstart on a running job is a no-op and unsupervised spawns
// fail closed on the agent lock.
func startAgentAvailabilityWatchdog(ctx context.Context, client *localagent.Client) {
	if client == nil || localagent.DisabledByEnv() {
		return
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return
	}
	go func() {
		failures := 0
		backoff := agentWatchdogRecoveryBackoff
		ticker := time.NewTicker(agentWatchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pingCtx, cancel := context.WithTimeout(ctx, time.Second)
			err := client.Ping(pingCtx)
			cancel()
			if err == nil {
				failures = 0
				backoff = agentWatchdogRecoveryBackoff
				continue
			}
			failures++
			if failures < agentWatchdogFailThreshold {
				continue
			}
			failures = 0
			// An orphaned runtime whose agent home was deleted (for example a
			// leaked harness worktree) must never keep spawning agents: a
			// recovered agent for a dead home reaps the machine's real router
			// owner and turns into a restart storm.
			if _, statErr := os.Stat(paths.Home); statErr != nil {
				agentWatchdogLog.report(os.Stderr, paths.SocketPath, "agent home is gone; stopping agent watchdog", statErr)
				return
			}
			agentWatchdogLog.report(os.Stderr, paths.SocketPath, "scenery agent unreachable; starting recovery", err)
			if startErr := agentWatchdogStartFunc(paths, localagent.StartOptions{}); startErr != nil {
				agentWatchdogLog.report(os.Stderr, paths.SocketPath, "scenery agent recovery start failed", startErr)
			}
			// Recovery that is not converging must never stop the watchdog
			// outright — a long external outage (an agent restart storm, a
			// wedged launchd job) would otherwise permanently disable the
			// only unattended recovery path. Back off exponentially instead.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, agentWatchdogRecoveryBackoffMax)
		}
	}()
}
