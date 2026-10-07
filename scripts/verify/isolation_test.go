package main

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/envpolicy"
)

func TestVerificationOwnsFallbackHomeAndPreservesNarrowerProbeHome(t *testing.T) {
	personal := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", personal)
	t.Setenv("SCENERY_AGENT_SOCKET", filepath.Join(personal, "agent.sock"))
	t.Setenv("SCENERY_EXECUTION_PURPOSE", "development")
	repo := t.TempDir()
	cleanup, err := isolateVerification(repo, "lane", harnessSelfModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	home := envpolicy.Get("SCENERY_AGENT_HOME")
	if home == personal || envpolicy.Get("SCENERY_AGENT_SOCKET") != "" || envpolicy.Get("SCENERY_EXECUTION_PURPOSE") != "verification" {
		t.Fatal("lane inherited personal state or purpose")
	}
	if err := os.WriteFile(filepath.Join(home, "telemetry.jsonl"), []byte("fixture evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	narrower := t.TempDir()
	restore := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(narrower)})
	if envpolicy.Get("SCENERY_AGENT_HOME") != narrower {
		t.Fatal("probe could not select its own home")
	}
	restore()
	cleanup()
	if envpolicy.Get("SCENERY_AGENT_HOME") != personal || envpolicy.Get("SCENERY_EXECUTION_PURPOSE") != "development" {
		t.Fatal("lane did not restore caller environment")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("disposable home retained: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, ".scenery", "harness", "artifacts", "lane", "verification-telemetry.jsonl")); err != nil || string(data) != "fixture evidence\n" {
		t.Fatalf("command evidence = %q: %v", data, err)
	}
}
