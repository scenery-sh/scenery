package main

import (
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pbrazdil/onlava/internal/neonselfhost"
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

func closedLoopbackPortForTest(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return port
}

func forceNeonCellPortsForTest(t *testing.T, port int) {
	t.Helper()
	root, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	state, ok, err := readNeonCellState(root)
	if err != nil || !ok {
		t.Fatalf("read Neon cell state ok=%v err=%v", ok, err)
	}
	for key := range state.Ports {
		state.Ports[key] = port
	}
	if err := writeNeonCellState(state); err != nil {
		t.Fatalf("write Neon cell state: %v", err)
	}
}

func forceNeonBackendBranchPortForTest(t *testing.T, project, branch string, port int) {
	t.Helper()
	root, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	path := filepath.Join(root, "backend.json")
	state, ok, err := neonselfhost.ReadBackendState(path)
	if err != nil {
		t.Fatalf("read Neon backend state: %v", err)
	}
	if !ok {
		state = neonselfhost.NewBackendState()
	}
	branch = normalizeNeonBranchName(branch)
	backendProject := state.Projects[project]
	if backendProject.Branches == nil {
		backendProject = neonselfhost.NewBackendProject(project, 16)
	}
	backendProject.Branches[neonLocalBranchID(project, branch)] = neonselfhost.BackendBranch{
		Project:  project,
		Branch:   branch,
		Host:     "127.0.0.1",
		Port:     port,
		Database: sanitizeNeonIdentifier(project),
		Role:     neonDefaultRole,
		Status:   "pending",
	}
	state.Projects[project] = backendProject
	if err := neonselfhost.WriteBackendState(path, state); err != nil {
		t.Fatalf("write Neon backend state: %v", err)
	}
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
