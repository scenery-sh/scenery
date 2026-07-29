package main

import (
	"context"
	"os"
	"time"

	localagent "scenery.sh/internal/agent"
)

var agentWatchdogLog = newComponentFailureLog("scenery-agent-watchdog", 30*time.Second, time.Now)

// agentWatchdogPolicy is the watchdog's timing and recovery seam. It is passed
// explicitly rather than held in package state so two watchdogs can run with
// different settings in one process.
type agentWatchdogPolicy struct {
	Interval           time.Duration
	FailThreshold      int
	RecoveryBackoff    time.Duration
	RecoveryBackoffMax time.Duration
	Start              func(localagent.Paths, localagent.StartOptions) error
}

func defaultAgentWatchdogPolicy() agentWatchdogPolicy {
	return agentWatchdogPolicy{
		Interval:           2 * time.Second,
		FailThreshold:      2,
		RecoveryBackoff:    10 * time.Second,
		RecoveryBackoffMax: 5 * time.Minute,
		Start:              localagent.StartProcess,
	}
}

func (p agentWatchdogPolicy) orDefault() agentWatchdogPolicy {
	d := defaultAgentWatchdogPolicy()
	if p.Interval <= 0 {
		p.Interval = d.Interval
	}
	if p.FailThreshold <= 0 {
		p.FailThreshold = d.FailThreshold
	}
	if p.RecoveryBackoff <= 0 {
		p.RecoveryBackoff = d.RecoveryBackoff
	}
	if p.RecoveryBackoffMax <= 0 {
		p.RecoveryBackoffMax = d.RecoveryBackoffMax
	}
	if p.Start == nil {
		p.Start = d.Start
	}
	return p
}

// startAgentAvailabilityWatchdog keeps the local agent alive from inside the
// long-running dev supervisor. launchd can pend the supervised gui
// LaunchAgent's KeepAlive respawn indefinitely ("pended nondemand spawn"), so
// a crashed agent is not guaranteed to come back on its own; every live
// `scenery up` supervisor therefore recovers a dead agent with an explicit
// demand start — a launchd kickstart when the supervisor plist owns the
// socket, an ordinary lock-protected spawn otherwise. Concurrent watchdogs
// are safe: kickstart on a running job is a no-op and unsupervised spawns
// fail closed on the agent lock.
// The returned channel closes once the watchdog goroutine has exited, so a
// caller that cancels ctx can wait for the loop to stop touching shared state
// instead of racing it.
func startAgentAvailabilityWatchdog(ctx context.Context, client *localagent.Client, paths localagent.Paths, policy agentWatchdogPolicy) <-chan struct{} {
	policy = policy.orDefault()
	stopped := make(chan struct{})
	if client == nil || localagent.DisabledByEnv() {
		close(stopped)
		return stopped
	}
	go func() {
		defer close(stopped)
		failures := 0
		backoff := policy.RecoveryBackoff
		ticker := time.NewTicker(policy.Interval)
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
				backoff = policy.RecoveryBackoff
				continue
			}
			failures++
			if failures < policy.FailThreshold {
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
			if startErr := policy.Start(paths, localagent.StartOptions{}); startErr != nil {
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
			backoff = min(backoff*2, policy.RecoveryBackoffMax)
		}
	}()
	return stopped
}
