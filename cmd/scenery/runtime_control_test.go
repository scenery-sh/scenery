package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/stateupgrade"
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

func TestResolveStatusAppRootPreservesLiteralPathWhitespace(t *testing.T) {
	fixture, home := canonicalTestDir(t), canonicalTestDir(t)
	t.Chdir(fixture)
	t.Setenv("SCENERY_AGENT_HOME", home)
	writeTestAppFile(t, fixture, ".scenery.json", `{"name":"enclosing"}`)
	writeTestAppFile(t, filepath.Join(fixture, "app"), ".scenery.json", `{"name":"alternate"}`)
	for _, name := range []string{"app ", "app\t", "app\n", " app", " "} {
		root := filepath.Join(fixture, name)
		writeTestAppFile(t, root, ".scenery.json", `{"name":"selected"}`)
		paths, err := commandWorktreePaths(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
			t.Fatal(err)
		}
		// Path selection must not decode retained mutation authority.
		if err := os.WriteFile(paths.Record, []byte("invalid retained sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, selected := range []string{root, root + string(filepath.Separator), name} {
			got, err := resolveStatusAppRoot(selected)
			if err != nil || got != paths.AppRoot {
				t.Errorf("literal selection %q = %q, %v; want %q", selected, got, err, paths.AppRoot)
			}
		}
	}
}

func TestDiscoverRuntimeAppIdentityPreservesSelectedScope(t *testing.T) {
	home := canonicalTestDir(t)
	t.Setenv("SCENERY_AGENT_HOME", home)
	parent := canonicalTestDir(t)
	writeTestAppFile(t, parent, ".scenery.json", `{"name":"parent","id":"parent"}`)
	alias := filepath.Join(canonicalTestDir(t), "parent")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ordinary-subdirectory", "missing-child", "symlinked-missing-child", "deleted-retained", "malformed-retained", "older-producer", "invalid-json", "foreign-root", "foreign-user", "malformed-unstarted", "pending-current", "pending-older"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(parent, name)
			paths, err := commandWorktreePaths(root)
			if err != nil {
				t.Fatal(err)
			}
			selected, wantRoot, wantID := root, root, "retained-app"
			wantError := ""
			files := map[string][]byte{}
			switch name {
			case "ordinary-subdirectory":
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				wantRoot, wantID = parent, "parent"
			case "missing-child", "symlinked-missing-child":
				wantError = "missing"
				if name == "symlinked-missing-child" {
					selected = filepath.Join(alias, name)
				}
			case "malformed-unstarted":
				writeTestAppFile(t, root, ".scenery.json", `{"name":`)
				wantError = "config"
			default:
				if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
					t.Fatal(err)
				}
				record := localagent.NewWorktreeRecord(paths, wantID)
				if name == "older-producer" || name == "pending-older" {
					record.SpecRevision = "sha256:" + strings.Repeat("a", 64)
				}
				if name == "foreign-root" {
					record.AppRoot = parent
					wantError = "retained"
				}
				if name == "foreign-user" {
					record.UserID++
					wantError = "retained"
				}
				data, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if name == "invalid-json" {
					data, wantError = []byte("invalid retained JSON"), "retained"
				}
				files[paths.Record] = data
				if name == "malformed-retained" {
					writeTestAppFile(t, root, ".scenery.json", `{"name":`)
				}
				if strings.HasPrefix(name, "pending-") {
					writeTestAppFile(t, root, ".scenery.json", `{"name":"desired-app"}`)
					files[filepath.Join(paths.Directory, stateupgrade.PendingName)] = []byte("pending publication guard")
					wantError = "pending"
				}
				for path, data := range files {
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			gotRoot, gotID, err := discoverRuntimeAppIdentity(selected)
			switch wantError {
			case "":
				if err != nil || gotRoot != wantRoot || gotID != wantID {
					t.Fatalf("identity = %q, %q, %v; want %q, %q", gotRoot, gotID, err, wantRoot, wantID)
				}
			case "missing":
				if !errors.Is(err, app.ErrRootNotFound) || cliExitCode(err) != 2 || !strings.Contains(err.Error(), root) {
					t.Fatalf("missing child classification/scope: %v", err)
				}
			case "config":
				if _, ok := errors.AsType[*app.ConfigError](err); !ok {
					t.Fatalf("malformed config classification: %v", err)
				}
			case "pending":
				if !errors.Is(err, stateupgrade.ErrPrecondition) || cliExitCode(err) != 3 {
					t.Fatalf("pending upgrade refusal: %v", err)
				}
			case "retained":
				if err == nil || gotRoot != "" || gotID != "" {
					t.Fatalf("invalid identity fell back to config: %q, %q, %v", gotRoot, gotID, err)
				}
			}
			entries, readErr := os.ReadDir(paths.Directory)
			if len(files) == 0 {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("reader allocated retained state: %v", readErr)
				}
			} else if readErr != nil || len(entries) != len(files) {
				t.Fatalf("reader changed retained directory: %v, %v", entries, readErr)
			}
			for path, before := range files {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("reader changed %s: %v", path, err)
				}
			}
		})
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
