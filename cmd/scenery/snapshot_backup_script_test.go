package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotBackupScriptVerifiesReplicatesThenPrunes(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	output := filepath.Join(root, "backups")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls")
	writeExecutable := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable("scenery", `#!/bin/sh
printf 'scenery %s\n' "$*" >> "$CALLS"
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then output="$2"; break; fi
  shift
done
if [ -n "$output" ]; then : > "$output"; fi
`)
	writeExecutable("rclone", `#!/bin/sh
printf 'rclone %s\n' "$*" >> "$CALLS"
`)
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"snapshot-20260101T000000Z.zip", "snapshot-20260102T000000Z.zip"} {
		if err := os.WriteFile(filepath.Join(output, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	script := filepath.Join("..", "..", "scripts", "snapshot-backup.sh")
	cmd := exec.Command("bash", script, "--app-root", "/tmp/app", "--output-dir", output, "--keep", "2", "--copy-to", "remote:app")
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CALLS="+calls)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snapshot backup: %v\n%s", err, combined)
	}
	entries, err := filepath.Glob(filepath.Join(output, "snapshot-*.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("retained snapshots = %v, want 2", entries)
	}
	if _, err := os.Stat(filepath.Join(output, "snapshot-20260101T000000Z.zip")); !os.IsNotExist(err) {
		t.Fatalf("oldest snapshot was not pruned: %v", err)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "snapshot save") || !strings.Contains(lines[1], "snapshot verify") || !strings.Contains(lines[2], "rclone copyto --checksum") {
		t.Fatalf("calls = %q", lines)
	}
}
