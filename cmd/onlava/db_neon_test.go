package main

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var neonDockerCommandTestMu sync.Mutex

func useFakeNeonDocker(t *testing.T, path string) {
	t.Helper()
	neonDockerCommandTestMu.Lock()
	previousDockerCommand := neonDockerCommand
	neonDockerCommand = path
	t.Cleanup(func() {
		neonDockerCommand = previousDockerCommand
		neonDockerCommandTestMu.Unlock()
	})
}

func useMissingNeonDocker(t *testing.T) {
	t.Helper()
	useFakeNeonDocker(t, filepath.Join(t.TempDir(), "missing-docker"))
}

func markNeonLeaseReadyForTest(t *testing.T, pin worktreeDBPin, endpoint neonEndpoint) {
	t.Helper()
	root, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	for i := range registry.Leases {
		if sameNeonLease(registry.Leases[i].Pin, pin) {
			registry.Leases[i].Status = "ready"
			registry.Leases[i].Endpoint = &endpoint
			registry.Leases[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := writeNeonBranchRegistry(root, registry); err != nil {
				t.Fatalf("write registry: %v", err)
			}
			return
		}
	}
	t.Fatalf("lease not found for pin %+v in %+v", pin, registry.Leases)
}

func neonPinForTest(project, branch, sessionID string) worktreeDBPin {
	return worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfhostProvider,
		Project:       project,
		ParentBranch:  "main",
		Branch:        branch,
		BranchID:      neonLocalBranchID(project, branch),
		Database:      project,
		Role:          neonDefaultRole,
		SessionID:     sessionID,
		CreatedBy:     "onlava",
		TTL:           neonDefaultTTL,
	}
}
