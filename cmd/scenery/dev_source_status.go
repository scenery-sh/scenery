package main

import (
	"context"
	"time"
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

// currentServingState is the identity the running host serves and how fresh
// it is against the current source.
func (s *devSupervisor) currentServingState(ctx context.Context) (*runtimeServingIdentity, string) {
	s.mu.RLock()
	generation, block := s.host, s.buildBlock
	s.mu.RUnlock()
	if _, source := worktreeSourceStatus(s.root); source == "missing" {
		return nil, "source_missing"
	}
	if generation == nil {
		if block != nil {
			return nil, "blocked"
		}
		return nil, "unknown"
	}
	var status runtimeSourceWait
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := readWorkerSourceStatus(probe, generation.endpoint, 0, &status); err != nil {
		return nil, "unknown"
	}
	return status.Serving, status.SourceFreshness
}
