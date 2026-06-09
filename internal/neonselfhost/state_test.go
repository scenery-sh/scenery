package neonselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.json")
	state := newTestBackendState("onlv", "tenant-test", 16)
	state.Projects["onlv"].Branches["br-local-a"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/a",
		TimelineID:       "timeline-a",
		ParentTimelineID: "timeline-main",
		EndpointID:       "feature-a",
		ComputeContainer: "onlava-neon-compute-feature-a",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(path, state); err != nil {
		t.Fatalf("WriteBackendState returned error: %v", err)
	}
	got, ok, err := ReadBackendState(path)
	if err != nil {
		t.Fatalf("ReadBackendState returned error: %v", err)
	}
	if !ok {
		t.Fatal("ReadBackendState ok=false")
	}
	if got.SchemaVersion != BackendSchemaVersion || got.Provider != "neon-selfhost" || got.Projects["onlv"].TenantID != "tenant-test" {
		t.Fatalf("state = %+v", got)
	}
	if got.Projects["onlv"].Branches["br-local-a"].Port != 55441 {
		t.Fatalf("branch = %+v", got.Projects["onlv"].Branches["br-local-a"])
	}
	if got.UpdatedAt == "" {
		t.Fatalf("updated_at was not set: %+v", got)
	}
}

func TestReadBackendStateRejectsBadState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"wrong","provider":"neon-selfhost","tenant_id":"t","default_pg_version":16,"branches":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadBackendState(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("schema error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":"onlava.db.neon.selfhost.backend.v1","provider":"other","tenant_id":"t","default_pg_version":16,"branches":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = ReadBackendState(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("provider error = %v", err)
	}
}

func TestReadBackendStateMigratesV1ToProjectState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": "onlava.db.neon.selfhost.backend.v1",
  "provider": "neon-selfhost",
  "tenant_id": "tenant-original",
  "default_pg_version": 16,
  "branches": {
    "br-local-a": {
      "project": "onlv",
      "branch": "shared",
      "timeline_id": "timeline-a",
      "endpoint_id": "shared",
      "compute_container": "onlava-neon-compute-onlv-a",
      "host": "127.0.0.1",
      "port": 55441,
      "database": "onlv",
      "role": "cloud_admin",
      "status": "ready"
    },
    "br-local-b": {
      "project": "other",
      "branch": "shared",
      "timeline_id": "timeline-b",
      "endpoint_id": "shared",
      "compute_container": "onlava-neon-compute-other-b",
      "host": "127.0.0.1",
      "port": 55442,
      "database": "other",
      "role": "cloud_admin",
      "status": "ready"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, ok, err := ReadBackendState(path)
	if err != nil {
		t.Fatalf("ReadBackendState returned error: %v", err)
	}
	if !ok {
		t.Fatal("ReadBackendState ok=false")
	}
	if state.SchemaVersion != BackendSchemaVersion || len(state.Projects) != 2 {
		t.Fatalf("state = %+v", state)
	}
	if state.Projects["onlv"].TenantID != "tenant-original" {
		t.Fatalf("onlv tenant = %q", state.Projects["onlv"].TenantID)
	}
	if state.Projects["other"].TenantID == "" || state.Projects["other"].TenantID == "tenant-original" {
		t.Fatalf("other tenant = %q", state.Projects["other"].TenantID)
	}
	if state.Projects["onlv"].Branches["br-local-a"].Branch != "shared" || state.Projects["other"].Branches["br-local-b"].Branch != "shared" {
		t.Fatalf("branches = %+v", state.Projects)
	}
}

func TestAllocateBranchPortStableAndCollisionAware(t *testing.T) {
	state := newTestBackendState("onlv", "tenant-test", 16)
	first, err := AllocateBranchPort(state, "onlv", "br-local-a")
	if err != nil {
		t.Fatalf("AllocateBranchPort returned error: %v", err)
	}
	if first < DefaultBranchPortBase || first >= DefaultBranchPortBase+DefaultBranchPortRange {
		t.Fatalf("first port out of range: %d", first)
	}
	state.Projects["onlv"].Branches["br-local-a"] = BackendBranch{Port: first}
	if got, err := AllocateBranchPort(state, "onlv", "br-local-a"); err != nil || got != first {
		t.Fatalf("existing branch port = %d, want %d", got, first)
	}
	collidingBranchID := "br-local-collision"
	collidingStart := DefaultBranchPortBase + int(hashString("onlv\x00"+collidingBranchID)%DefaultBranchPortRange)
	state.Projects["onlv"].Branches["br-local-b"] = BackendBranch{Port: collidingStart}
	next, err := AllocateBranchPort(state, "onlv", collidingBranchID)
	if err != nil {
		t.Fatalf("AllocateBranchPort collision returned error: %v", err)
	}
	if next == collidingStart {
		t.Fatalf("allocated colliding port %d", next)
	}
	if next < DefaultBranchPortBase || next >= DefaultBranchPortBase+DefaultBranchPortRange {
		t.Fatalf("next port out of range: %d", next)
	}
}

func TestBackendBranchComputeContainerUsesProjectAndBranchID(t *testing.T) {
	state := NewBackendState()
	firstProject := NewBackendProject("app-a", 16)
	state.Projects["app-a"] = firstProject
	secondProject := NewBackendProject("app-b", 16)
	state.Projects["app-b"] = secondProject
	first, err := backendBranchFromOptions(state, firstProject, "app-a", branchActionOptions{
		Project:  "app-a",
		Branch:   "feature/foo",
		BranchID: "br-local-1111111111111111",
		Database: "app_a",
		Role:     "cloud_admin",
	})
	if err != nil {
		t.Fatalf("first backendBranchFromOptions returned error: %v", err)
	}
	second, err := backendBranchFromOptions(state, secondProject, "app-b", branchActionOptions{
		Project:  "app-b",
		Branch:   "feature/foo",
		BranchID: "br-local-2222222222222222",
		Database: "app_b",
		Role:     "cloud_admin",
	})
	if err != nil {
		t.Fatalf("second backendBranchFromOptions returned error: %v", err)
	}
	if first.ComputeContainer != "onlava-neon-compute-app-a-111111111111" {
		t.Fatalf("first compute container = %q", first.ComputeContainer)
	}
	if second.ComputeContainer != "onlava-neon-compute-app-b-222222222222" {
		t.Fatalf("second compute container = %q", second.ComputeContainer)
	}
	if first.ComputeContainer == second.ComputeContainer {
		t.Fatalf("compute containers collided: %q", first.ComputeContainer)
	}
}

func TestTwoProjectsSameBranchNameUseDifferentTenantComputeAndPorts(t *testing.T) {
	state := NewBackendState()
	projectA, projectNameA, err := ensureBackendProject(&state, "app-a", 16)
	if err != nil {
		t.Fatalf("ensure app-a project: %v", err)
	}
	projectB, projectNameB, err := ensureBackendProject(&state, "app-b", 16)
	if err != nil {
		t.Fatalf("ensure app-b project: %v", err)
	}
	branchA, err := backendBranchFromOptions(state, projectA, projectNameA, branchActionOptions{
		Project:  "app-a",
		Branch:   "same-branch",
		BranchID: "br-local-same-suffix",
		Database: "app_a",
		Role:     "cloud_admin",
	})
	if err != nil {
		t.Fatalf("branch app-a: %v", err)
	}
	projectA.Branches["br-local-same-suffix"] = branchA
	state.Projects[projectNameA] = projectA
	branchB, err := backendBranchFromOptions(state, projectB, projectNameB, branchActionOptions{
		Project:  "app-b",
		Branch:   "same-branch",
		BranchID: "br-local-same-suffix",
		Database: "app_b",
		Role:     "cloud_admin",
	})
	if err != nil {
		t.Fatalf("branch app-b: %v", err)
	}
	ensureBackendIDs(&projectA, &branchA, projectNameA, branchActionOptions{Project: "app-a", Branch: "same-branch", BranchID: "br-local-same-suffix"})
	ensureBackendIDs(&projectB, &branchB, projectNameB, branchActionOptions{Project: "app-b", Branch: "same-branch", BranchID: "br-local-same-suffix"})
	if projectA.TenantID == projectB.TenantID {
		t.Fatalf("tenant IDs collided: %q", projectA.TenantID)
	}
	if branchA.ComputeContainer == branchB.ComputeContainer {
		t.Fatalf("compute containers collided: %q", branchA.ComputeContainer)
	}
	if branchA.Port == branchB.Port {
		t.Fatalf("ports collided: %d", branchA.Port)
	}
}
