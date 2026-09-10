package main

import (
	"os"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestResolveStatusAppRootUsesMarkerWhenDesiredConfigIsMalformed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"validation":{"profiles":{"quick":{"commands":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	got, err := resolveStatusAppRoot("")
	if err != nil {
		t.Fatalf("resolveStatusAppRoot returned error: %v", err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolveStatusAppRoot = %q, want %q", got, want)
	}
}

func TestDiscoverRuntimeAppIdentityUsesRetainedRecordForMalformedConfig(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"validation":{"profiles":{"quick":{"commands":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Prepare(); err != nil {
		t.Fatal(err)
	}
	op, err := paths.BeginOperation()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.Close() })
	if err := op.SaveRecord(localagent.NewWorktreeRecord(paths, "retained-app")); err != nil {
		t.Fatal(err)
	}

	gotRoot, gotID, err := discoverRuntimeAppIdentity(root)
	if err != nil {
		t.Fatalf("discoverRuntimeAppIdentity returned error: %v", err)
	}
	if gotRoot != paths.AppRoot || gotID != "retained-app" {
		t.Fatalf("discoverRuntimeAppIdentity = %q, %q; want %q, %q", gotRoot, gotID, paths.AppRoot, "retained-app")
	}
}
