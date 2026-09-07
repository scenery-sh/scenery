package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestWorktreeDownAbsentDoesNotAllocate(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	root := t.TempDir()
	var output bytes.Buffer
	if err := runWorktreeDown(t.Context(), &output, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Directory); !os.IsNotExist(err) {
		t.Fatalf("absent down allocated durable state: %v", err)
	}
}

func TestWorktreeCleanupRefusesExternalDSNWithoutRetainedState(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://user:secret@127.0.0.1:5432/demo")
	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DATABASE_URL=postgres://user:secret@127.0.0.1:5432/demo\n")
	var output bytes.Buffer
	for _, err := range []error{
		runWorktreeDown(t.Context(), &output, []string{"--app-root", root, "--db"}),
		runWorktreePrune(t.Context(), &output, []string{"--app-root", root, "--older-than", "1h", "--db"}),
	} {
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is external") {
			t.Fatalf("cleanup error = %v", err)
		}
	}
}

func TestStandaloneDatabaseLifecycleRefusesLiveOwnerBeforeProvision(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	paths, err := commandWorktreePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := paths.AcquireLiveLock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	cfg := app.Config{Name: "demo", Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{"db": {}}}}
	_, _, err = beginDatabaseLifecycleEnv(t.Context(), root, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "live owner") {
		t.Fatalf("lifecycle error = %v", err)
	}
	if _, err := os.Stat(paths.Record); !os.IsNotExist(err) {
		t.Fatalf("conflict allocated authority: %v", err)
	}
}
