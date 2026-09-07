package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/machine"
)

func TestLegacyDatabaseAuthorityWithoutSessionsBlocksFreshAllocation(t *testing.T) {
	t.Parallel()
	machine := PathsForHome(t.TempDir())
	worktree, err := PathsForWorktree(machine.Home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(machine.AgentDir, "postgres", "server.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("opaque old database authority; not a current JSON decoder input")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyWorktreeClaim(machine, worktree); err == nil {
		t.Fatal("retained database without leases was treated as fresh")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(old, after) {
		t.Fatal("legacy authority was rewritten")
	}
	if _, err := os.Stat(worktree.Directory); !os.IsNotExist(err) {
		t.Fatalf("guard allocated new authority: %v", err)
	}
}

func TestLegacyRootProvenanceDoesNotClaimUnrelatedWorktrees(t *testing.T) {
	t.Parallel()
	machinePaths := PathsForHome(t.TempDir())
	source := t.TempDir()
	fresh, err := PathsForWorktree(machinePaths.Home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(machinePaths.AgentDir, "postgres"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machinePaths.AgentDir, "postgres/server.json"), []byte("credentials remain opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := registryFile{
		ArtifactIdentity: machine.NewArtifactIdentity(AgentRegistryKind, `{"identity":"artifact","registry":"sessions-substrates-aliases"}`),
		Sessions:         []Session{{AppRoot: source}},
	}
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machinePaths.RegistryPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyWorktreeClaim(machinePaths, fresh); err != nil {
		t.Fatalf("unrelated fresh root depends on the old shared runtime: %v", err)
	}
	claimed, err := PathsForWorktree(machinePaths.Home, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyWorktreeClaim(machinePaths, claimed); err == nil {
		t.Fatal("selected old root lost its migration precondition")
	}
	after, err := os.ReadFile(machinePaths.RegistryPath)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("provenance inspection rewrote the old registry")
	}
	registry.SchemaRevision = "unknown-schema"
	data, err = json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machinePaths.RegistryPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyWorktreeClaim(machinePaths, fresh); err == nil {
		t.Fatal("unknown ownership provenance was guessed")
	}
}
