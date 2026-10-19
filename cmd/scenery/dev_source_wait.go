package main

import (
	"context"
	"time"

	"scenery.sh/internal/workspacetx"
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
}

// runtimeSourceWait is the `wait` result; ok is true exactly when the
// target was served. Outcome is one of served, failed,
// blocked, superseded, source_missing or timeout. A timeout only ends this
// wait; the build may still serve the target later.
type runtimeSourceWait struct {
	cliPayloadIdentity
	OK                          bool                    `json:"ok"`
	Outcome                     string                  `json:"outcome"`
	TargetSourceSnapshotDigest  string                  `json:"target_source_snapshot_digest"`
	CurrentSourceSnapshotDigest string                  `json:"current_source_snapshot_digest,omitempty"`
	Serving                     *runtimeServingIdentity `json:"serving,omitempty"`
	SourceFreshness             string                  `json:"source_freshness"`
	CompileError                string                  `json:"compile_error,omitempty"`
	BuildBlock                  *runtimeBuildBlock      `json:"build_block,omitempty"`
	WaitedMS                    int64                   `json:"waited_ms"`
}

// waitForSourceSnapshot captures the watched source as it is now and waits
// until a generation built from exactly that snapshot serves, its build
// fails or is blocked, a later edit supersedes it, or timeout elapses.
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
	finish := func(outcome string) (runtimeSourceWait, error) {
		result.Outcome, result.OK = outcome, outcome == "served"
		result.WaitedMS = time.Since(started).Milliseconds()
		return result, nil
	}
	if _, source := worktreeSourceStatus(s.root); source == "missing" {
		result.SourceFreshness = "source_missing"
		return finish("source_missing")
	}
	s.mu.RLock()
	base := s.servingSnapshot
	s.mu.RUnlock()
	previous := fileSnapshot{}
	if base != nil {
		previous = *base
	}
	var target fileSnapshot
	for {
		var err error
		target, err = scanWatchedFilesReusing(s.root, previous)
		if err == nil {
			break
		}
		if !workspacetx.IsActive(err) {
			return runtimeSourceWait{}, err
		}
		// A source/generated transaction is publishing; capture after it.
		select {
		case <-ctx.Done():
			result.SourceFreshness = "transaction_pending"
			return finish("timeout")
		case <-time.After(poll):
		}
	}
	result.TargetSourceSnapshotDigest = snapshotFingerprint(target)
	for {
		identity, freshness := s.currentServingState(ctx)
		result.Serving, result.SourceFreshness = identity, freshness
		if identity != nil && identity.SourceSnapshotDigest == result.TargetSourceSnapshotDigest {
			result.SourceFreshness = "current"
			return finish("served")
		}
		s.mu.RLock()
		attempt, block := s.lastBuild, s.buildBlock
		s.mu.RUnlock()
		if attempt != nil && attempt.SourceSnapshotDigest == result.TargetSourceSnapshotDigest && attempt.Error != "" {
			result.CompileError = attempt.Error
			return finish("failed")
		}
		if block != nil {
			result.BuildBlock = &runtimeBuildBlock{Reason: block.Reason, Cause: block.Cause, Since: block.Since.Format(time.RFC3339Nano), PreventedBuilds: block.Prevented}
			return finish("blocked")
		}
		if freshness == "source_missing" {
			return finish("source_missing")
		}
		current, err := scanWatchedFilesReusing(s.root, target)
		if err == nil {
			if digest := snapshotFingerprint(current); digest != result.TargetSourceSnapshotDigest {
				result.CurrentSourceSnapshotDigest = digest
				return finish("superseded")
			}
			target = current
		} else if !workspacetx.IsActive(err) {
			return runtimeSourceWait{}, err
		}
		select {
		case <-ctx.Done():
			return finish("timeout")
		case <-time.After(poll):
		}
	}
}
