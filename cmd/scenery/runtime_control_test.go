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

func TestResolveStatusAppRootPreservesExplicitRetainedScope(t *testing.T) {
	home := canonicalTestDir(t)
	t.Setenv("SCENERY_AGENT_HOME", home)
	parent := canonicalTestDir(t)
	writeTestAppFile(t, parent, ".scenery.json", `{"name":"parent","id":"parent"}`)
	alias := filepath.Join(canonicalTestDir(t), "parent")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		exists   bool
		retained bool
		record   string
		alias    bool
	}{
		{name: "missing-checkout"},
		{name: "missing-through-symlink", alias: true},
		{name: "ordinary-subdirectory", exists: true},
		{name: "invalid-retained-record", exists: true, retained: true, record: "invalid retained record"},
		{name: "pending-retained-record", exists: true, retained: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(parent, tc.name)
			if tc.exists {
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			paths, err := commandWorktreePaths(root)
			if err != nil {
				t.Fatal(err)
			}
			if tc.retained {
				if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
					t.Fatal(err)
				}
				if tc.record != "" {
					if err := os.WriteFile(paths.Record, []byte(tc.record), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			selected := root
			if tc.alias {
				selected = filepath.Join(alias, tc.name)
			}
			got, err := resolveStatusAppRoot(selected)
			want := root
			if tc.exists && !tc.retained {
				want = parent
			}
			if err != nil || got != want {
				t.Fatalf("resolveStatusAppRoot = %q, %v; want %q", got, err, want)
			}
			if !tc.retained {
				if _, err := os.Lstat(paths.Directory); !os.IsNotExist(err) {
					t.Fatalf("root selection allocated retained state: %v", err)
				}
			}
		})
	}
}
