package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/envpolicy"
)

func TestGoTestsCannotAllocateInTheFallbackHome(t *testing.T) {
	personal := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", personal)
	repo := t.TempDir()
	var previous []string
	for _, run := range []string{"first", "second"} {
		cleanup, err := isolateVerification(repo, run, harnessSelfModeDefault)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		home := envpolicy.Get("SCENERY_AGENT_HOME")
		overrides := harnessSelfGoTestEnv()
		if previous != nil && !slices.Equal(previous, overrides) {
			t.Fatalf("test environment changes between lanes: %v != %v", previous, overrides)
		}
		previous = overrides
		childEnv := envWithOverrides(envpolicy.Environ(), overrides...)
		fallback := ""
		for _, item := range childEnv {
			if value, ok := strings.CutPrefix(item, "SCENERY_AGENT_HOME="); ok {
				fallback = value
			}
		}
		if fallback != os.DevNull {
			t.Fatalf("test fallback = %q", fallback)
		}
		if err := os.MkdirAll(filepath.Join(fallback, "must-not-exist"), 0o700); err == nil {
			t.Fatal("test fallback allowed state allocation")
		}
		if envpolicy.Get("SCENERY_AGENT_HOME") != home || home == personal {
			t.Fatal("test override changed the enclosing private lane home")
		}
		cleanup()
	}
}

func TestVerificationOwnsFallbackHomeAndPreservesNarrowerProbeHome(t *testing.T) {
	personal := t.TempDir()
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	t.Setenv("SCENERY_AGENT_HOME", personal)
	t.Setenv("SCENERY_AGENT_SOCKET", filepath.Join(personal, "agent.sock"))
	t.Setenv("SCENERY_EXECUTION_PURPOSE", "development")
	repo := t.TempDir()
	cleanup, err := isolateVerification(repo, "lane", harnessSelfModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if envpolicy.Get("GIT_OPTIONAL_LOCKS") != "0" {
		t.Fatal("verification descendants can take optional Git index locks")
	}
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
	if envpolicy.Get("GIT_OPTIONAL_LOCKS") != "1" {
		t.Fatal("lane did not restore the caller's Git lock policy")
	}
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
