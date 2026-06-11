package main

// The edge trust tests spawn several processes per run; tests execute in file
// order, so this file's zz_ prefix runs them at the end of the sequential
// phase, after the suite-wide build storm has subsided.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestEdgeTrustUsesTemporaryCaddyAdmin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executable shell fixture is Unix-only")
	}
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("SCENERY_TOOLCHAIN_DIR", "")
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	caddy := filepath.Join(edgeToolchainStoreDir(paths), "artifacts", "caddy", "2.11.3", currentPlatformDirForTest(), "bin", "caddy")
	if err := os.MkdirAll(filepath.Dir(caddy), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	writeFakeTrustCaddy(t, caddy, marker)
	t.Setenv("SCENERY_FAKE_CADDY_MARKER", marker)

	if err := edgeTrust(edgeOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "run\n") || !strings.Contains(got, "trust\n") {
		t.Fatalf("fake Caddy marker = %q, want run and trust", got)
	}
}

func TestSystemTrustRunsEdgeTrust(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	caddy := filepath.Join(edgeToolchainStoreDir(paths), "artifacts", "caddy", "2.11.3", currentPlatformDirForTest(), "bin", "caddy")
	if err := os.MkdirAll(filepath.Dir(caddy), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTrustCaddy(t, caddy, filepath.Join(t.TempDir(), "marker"))
	if err := os.WriteFile(paths.EdgeConfigPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		t.Fatal("watcher should not run when system trust performs edge trust setup")
		return nil
	}

	err = systemCommand([]string{"trust"})
	if err != nil {
		t.Fatalf("system trust returned error: %v", err)
	}
}
