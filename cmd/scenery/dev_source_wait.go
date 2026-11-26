package main

import (
	"context"
	"time"
)

const (
	devRuntimeWaitKind       = "scenery.dev-runtime.wait"
	defaultSourceWaitTimeout = 30 * time.Second
	maxSourceWaitTimeout     = 120 * time.Second
	sourceWaitPollInterval   = 100 * time.Millisecond
)

// devBuildAttempt is the outcome of one build request for a captured source
// snapshot. Error is empty for a success and for a build that a newer
// snapshot superseded or a pending workspace transaction deferred.
type devBuildAttempt struct {
	SourceSnapshotDigest string
	Error                string
	Failure              *devBuildFailure
}

// runtimeSourceWait is the `wait` result; ok is true exactly when the
// target was served. Outcome is one of served, failed,
// blocked, superseded, source_missing or timeout. A timeout only ends this
// wait; the build may still serve the target later.
type runtimeSourceWait struct {
	cliPayloadIdentity
	OK                          bool                    `json:"ok"`
	Outcome                     string                  `json:"outcome"`
	TargetSourceSnapshotDigest  string                  `json:"target_source_snapshot_digest,omitempty"`
	CurrentSourceSnapshotDigest string                  `json:"current_source_snapshot_digest,omitempty"`
	Serving                     *runtimeServingIdentity `json:"serving,omitempty"`
	SourceFreshness             string                  `json:"source_freshness"`
	CompileError                string                  `json:"compile_error,omitempty"`
	BuildBlock                  *runtimeBuildBlock      `json:"build_block,omitempty"`
	BuildFailure                *devBuildFailure        `json:"build_failure,omitempty"`
	RebuildTiming               *workerRebuildTiming    `json:"rebuild_timing,omitempty"`
	WaitedMS                    int64                   `json:"waited_ms"`
}

// waitForSourceSnapshot waits until a generation built from the watched source
// as it is now serves, its build fails or is blocked, a later edit supersedes
// it, or timeout elapses. The running host owns that answer; before a host
// runs, a failed or blocked start ends the wait.
func (s *devSupervisor) waitForSourceSnapshot(ctx context.Context, timeout time.Duration) (runtimeSourceWait, error) {
	started := time.Now()
	if timeout <= 0 {
		timeout = defaultSourceWaitTimeout
	}
	timeout = min(timeout, maxSourceWaitTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	poll := s.sourceWaitPoll
	if poll <= 0 {
		poll = sourceWaitPollInterval
	}
	result := runtimeSourceWait{cliPayloadIdentity: newCLIPayloadIdentity(devRuntimeWaitKind), SourceFreshness: "unknown"}
	for {
		if _, source := worktreeSourceStatus(s.root); source == "missing" {
			result.SourceFreshness = "source_missing"
			return result.finish(started, "source_missing")
		}
		s.mu.RLock()
		generation, attempt, block := s.host, s.lastBuild, s.buildBlock
		s.mu.RUnlock()
		if generation != nil {
			var served runtimeSourceWait
			remaining := time.Until(started.Add(timeout))
			if err := readWorkerSourceStatus(ctx, generation.endpoint, remaining, &served); err == nil {
				return served, nil
			} else if ctx.Err() != nil {
				return result.finish(started, "timeout")
			}
		} else if attempt != nil && attempt.Error != "" {
			result.CompileError, result.BuildFailure = attempt.Error, attempt.Failure
			return result.finish(started, "failed")
		} else if block != nil {
			result.BuildBlock = &runtimeBuildBlock{Reason: block.Reason, Cause: block.Cause, Since: block.Since.Format(time.RFC3339Nano), PreventedBuilds: block.Prevented}
			return result.finish(started, "blocked")
		}
		select {
		case <-ctx.Done():
			return result.finish(started, "timeout")
		case <-time.After(poll):
		}
	}
}

func (result runtimeSourceWait) finish(started time.Time, outcome string) (runtimeSourceWait, error) {
	result.Outcome, result.OK = outcome, outcome == "served"
	result.WaitedMS = time.Since(started).Milliseconds()
	return result, nil
}
