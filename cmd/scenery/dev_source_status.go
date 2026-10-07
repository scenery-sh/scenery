package main

import (
	"context"
	"maps"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/workspacetx"
)

// Serving is published-process evidence. A newer candidate manifest on disk
// cannot replace this identity or make an unapplied source edit current.
type runtimeServingIdentity struct {
	Generation             uint64 `json:"generation"`
	PID                    string `json:"pid"`
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
	FrameworkSourceDigest  string `json:"framework_source_digest"`
	SourceSnapshotDigest   string `json:"source_snapshot_digest"`
	PublishedAt            string `json:"published_at"`
}

func (s *devSupervisor) captureServingState(snapshot fileSnapshot, result *build.Result) {
	if result == nil || result.BuildInput == nil || result.Target == nil || result.Contract == nil {
		return
	}
	identity := runtimeServingIdentity{
		ContractRevision:       result.Contract.Manifest.ContractRevision,
		ImplementationRevision: result.ImplementationRevisions[result.Target.Name],
		BuildInputDigest:       result.BuildInput.Digest, FrameworkSourceDigest: result.FrameworkSourceDigest,
		SourceSnapshotDigest: snapshotFingerprint(snapshot), PublishedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if s.processes != nil {
		s.processes.mu.Lock()
		identity.Generation = s.processes.generation
		s.processes.mu.Unlock()
	}
	// The watcher advances its own snapshots after generated publication.
	// These captured membership/hash maps remain immutable for status reads.
	snapshot.files = maps.Clone(snapshot.files)
	snapshot.compilerFiles = maps.Clone(snapshot.compilerFiles)
	snapshot.compilerImpl = maps.Clone(snapshot.compilerImpl)
	snapshot.compilerAbsent = maps.Clone(snapshot.compilerAbsent)
	snapshot.generatedContent = maps.Clone(snapshot.generatedContent)
	s.mu.Lock()
	identity.PID = s.status.PID
	s.servingSnapshot, s.servingIdentity = &snapshot, &identity
	s.mu.Unlock()
}

func (s *devSupervisor) currentServingState(ctx context.Context) (*runtimeServingIdentity, string) {
	s.mu.RLock()
	identity, snapshot, block, model := s.servingIdentity, s.servingSnapshot, s.buildBlock, s.processes
	s.mu.RUnlock()
	if identity != nil && model != nil {
		// Configuration changes and reconciliation can publish another generation
		// of the same source without passing through the build watcher.
		current := *identity
		model.mu.Lock()
		current.Generation = model.generation
		if model.host != nil && model.host.app != nil {
			current.PID = model.host.app.pid
		}
		model.mu.Unlock()
		identity = &current
	}
	_, source := worktreeSourceStatus(s.root)
	if source == "missing" {
		return identity, "source_missing"
	}
	if block != nil {
		return identity, "blocked"
	}
	if identity == nil || snapshot == nil || ctx.Err() != nil || source != "present" {
		return identity, "unknown"
	}
	current, err := scanWatchedFilesReusing(s.root, *snapshot)
	if workspacetx.IsActive(err) {
		return identity, "transaction_pending"
	}
	if err != nil {
		return identity, "unknown"
	}
	if snapshotFingerprint(current) != identity.SourceSnapshotDigest {
		return identity, "stale"
	}
	return identity, "current"
}
