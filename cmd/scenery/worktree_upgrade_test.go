package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/stateupgrade"
)

func TestWorktreeUpgradeKeepsExplicitRootBeforeAdmission(t *testing.T) {
	for _, test := range []struct {
		name      string
		suffix    string
		missing   bool
		retained  bool
		malformed bool
	}{
		{name: "missing marker", retained: true},
		{name: "missing checkout", missing: true, retained: true},
		{name: "missing checkout without state", missing: true},
		{name: "malformed marker", retained: true, malformed: true},
		{name: "missing marker trailing space", suffix: " ", retained: true},
		{name: "missing checkout trailing tab", suffix: "\t", missing: true, retained: true},
		{name: "malformed marker trailing newline", suffix: "\n", retained: true, malformed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := t.TempDir()
			parent, home := filepath.Join(fixture, "parent"), filepath.Join(fixture, "home")
			if err := os.MkdirAll(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(appcfg.ConfigPath(parent), []byte(`{"name":"parent","envs":{"local":{"default":true}}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(parent, "child"+test.suffix)
			if !test.missing {
				if err := os.Mkdir(child, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			paths, err := localagent.PathsForWorktree(home, child)
			if err != nil {
				t.Fatal(err)
			}
			if test.retained {
				if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
					t.Fatal(err)
				}
				// Selection must not decode either sentinel or bypass a pending
				// upgrade. Missing configuration refuses before mutation authority.
				for _, name := range []string{paths.Record, filepath.Join(paths.Directory, "spec-upgrade.json")} {
					if err := os.WriteFile(name, []byte("invalid retained sentinel"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if test.malformed {
				if err := os.WriteFile(appcfg.ConfigPath(child), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			agentPaths := localagent.PathsForHome(home)
			commandAgentPathsOverride = &agentPaths
			t.Cleanup(func() { commandAgentPathsOverride = nil })
			snapshot := func() map[string]string {
				files := map[string]string{}
				if err := filepath.WalkDir(fixture, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					files[path] = "directory"
					if !entry.IsDir() {
						data, err := os.ReadFile(path)
						if err != nil {
							return err
						}
						files[path] = string(data)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return files
			}
			before := snapshot()
			for _, apply := range []bool{false, true} {
				var output bytes.Buffer
				opts := worktreeOptions{AppRoot: child, JSON: true, Yes: apply, ExpectedRevision: "sha256:" + strings.Repeat("a", 64)}
				err := runWorktreeUpgrade(t.Context(), &output, opts)
				wantExit := 2
				if test.malformed {
					wantExit = 3
					if _, ok := errors.AsType[*appcfg.ConfigError](err); !ok {
						t.Fatalf("malformed selected config: %v", err)
					}
				} else if !errors.Is(err, appcfg.ErrRootNotFound) {
					t.Fatalf("missing selected config: %v", err)
				}
				if cliExitCode(err) != wantExit || !strings.Contains(err.Error(), paths.AppRoot) || output.Len() != 0 {
					t.Fatalf("selected-root refusal (apply=%t): %v, output %q", apply, err, output.String())
				}
				if !maps.Equal(before, snapshot()) {
					t.Fatal("refusal changed fixture bytes or allocated state")
				}
			}
		})
	}
}

func TestWorktreeUpgradeArgumentContract(t *testing.T) {
	preview, err := parseWorktreeArgs([]string{"upgrade", "--app-root", "/app", "-o", "json"})
	if err != nil || preview.Yes || !preview.JSON {
		t.Fatalf("read-only preview: %+v, %v", preview, err)
	}
	revision := "sha256:" + strings.Repeat("a", 64)
	apply, err := parseWorktreeArgs([]string{"upgrade", "--yes", "--expect-revision", revision})
	if err != nil || !apply.Yes || apply.ExpectedRevision != revision {
		t.Fatalf("explicit apply: %+v, %v", apply, err)
	}
	for _, args := range [][]string{
		{"upgrade", "--yes"}, {"upgrade", "--expect-revision", revision},
		{"upgrade", "name"}, {"upgrade", "--from", "main"},
		{"list", "--yes"}, {"remove", "name", "--expect-revision", revision},
	} {
		if _, err := parseWorktreeArgs(args); err == nil {
			t.Fatalf("unsupported arguments accepted: %v", args)
		}
	}
}

func TestWorktreeUpgradePreviewsAppliesAndValidatesSchema(t *testing.T) {
	paths, err := localagent.PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, acquire := range []func() (*localagent.ProcessLock, error){paths.AcquireLiveLock, paths.AcquireOperationLock} {
		lock, err := acquire()
		if err != nil {
			t.Fatal(err)
		}
		if err := lock.Release(); err != nil {
			t.Fatal(err)
		}
	}
	record := localagent.NewWorktreeRecord(paths, "upgrade-test")
	record.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	before, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Record, before, 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{})
	if err != nil || preview.ChangedFiles != 1 || preview.Applied || preview.Backup != "" {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	actual, err := os.ReadFile(paths.Record)
	if err != nil || !bytes.Equal(before, actual) {
		t.Fatal("preview changed worktree metadata")
	}
	if _, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{Yes: true, ExpectedRevision: "sha256:" + strings.Repeat("c", 64)}); !errors.Is(err, stateupgrade.ErrPrecondition) {
		t.Fatalf("stale approval accepted: %v", err)
	}
	lock, err := paths.AcquireLiveLock()
	if err != nil {
		t.Fatal(err)
	}
	_, busyErr := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{})
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(busyErr, localagent.ErrProcessLocked) {
		t.Fatalf("live owner accepted: %v", busyErr)
	}
	applied, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{Yes: true, ExpectedRevision: preview.Revision})
	if err != nil || !applied.Applied || applied.Pending || applied.UpdatedFiles != 1 || applied.Backup == "" {
		t.Fatalf("apply: %+v, %v", applied, err)
	}
	if _, err := paths.LoadRecord(record.AppID); err != nil {
		t.Fatalf("current reader rejected upgrade: %v", err)
	}
	current, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{})
	if err != nil || current.ChangedFiles != 0 {
		t.Fatalf("current no-op: %+v, %v", current, err)
	}
	for _, result := range []worktreeUpgradeResult{preview, applied, current} {
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", worktreeUpgradeKind+".schema.json"), result); len(diagnostics) != 0 {
			t.Fatalf("upgrade schema: %+v", diagnostics)
		}
	}
}
