package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPreRebrandProcessesRequireExactOwnedPaths(t *testing.T) {
	t.Parallel()

	home := filepath.Join(string(filepath.Separator), "Users", "test", ".onlava")
	uid := 501
	processes := []runtimeProcess{
		{PID: 10, UID: uid, Command: "/opt/caddy run --config " + filepath.Join(home, "agent", "edge", "Caddyfile") + " --adapter caddyfile"},
		{PID: 11, UID: uid, Command: "/opt/scenery system agent --socket " + filepath.Join(home, "run", "agent.sock")},
		{PID: 12, UID: uid, Command: "/opt/scenery system agent --socket=" + filepath.Join(home, "run", "agent.sock")},
		{PID: 13, UID: uid, Command: "/opt/scenery system agent --socket " + filepath.Join(string(filepath.Separator), "tmp", "agent.sock")},
		{PID: 14, UID: uid + 1, Command: "/opt/scenery system agent --socket " + filepath.Join(home, "run", "agent.sock")},
		{PID: 15, UID: uid, Command: "/opt/scenery system agent --socket " + filepath.Join(home, "run", "agent.sock")},
	}
	matches := preRebrandProcesses(processes, home, uid, 15)
	if len(matches) != 3 || matches[0].PID != 10 || matches[1].PID != 11 || matches[2].PID != 12 {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestRunAgentCleanupReportsStateBeforeExplicitRemoval(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".onlava")
	if err := os.MkdirAll(filepath.Join(legacy, "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runAgentCleanup(&stdout, legacy, agentCleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("default cleanup removed state: %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("--remove-state")) {
		t.Fatalf("cleanup output did not offer explicit state removal: %s", stdout.String())
	}
	stdout.Reset()
	if err := runAgentCleanup(&stdout, legacy, agentCleanupOptions{RemoveState: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("explicit cleanup left state: %v", err)
	}
}

func TestRemovePreRebrandStateRequiresOnlavaRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	legacy := filepath.Join(root, ".onlava")
	if err := os.MkdirAll(filepath.Join(legacy, "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removePreRebrandState(filepath.Join(root, ".scenery")); err == nil {
		t.Fatal("removePreRebrandState accepted non-.onlava path")
	}
	if err := removePreRebrandState(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy state still exists: %v", err)
	}
}

func TestParseAgentCleanupArgsRequiresExplicitStateRemoval(t *testing.T) {
	t.Parallel()

	opts, err := parseAgentCleanupArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.RemoveState {
		t.Fatal("state removal enabled by default")
	}
	opts, err = parseAgentCleanupArgs([]string{"--remove-state", "-o", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.RemoveState || !opts.JSON {
		t.Fatalf("cleanup options = %+v", opts)
	}
}
