package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

const harnessSnapshotBackupProbeName = "snapshot backup script probe"

type harnessSnapshotBackupCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessSnapshotBackupProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessSnapshotBackupProbeStepWithCheck(ctx, repoRoot, runHarnessSnapshotBackupProbeCheck)
}

func runHarnessSnapshotBackupProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessSnapshotBackupCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessSnapshotBackupProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the snapshot backup script boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessSnapshotBackupProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{
			"proof":  "not_applicable_on_windows",
			"reason": "snapshot-backup.sh is a POSIX operational script",
		}, nil, nil
	}
	root, err := os.MkdirTemp("", "scenery-snapshot-backup-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	bin := filepath.Join(root, "bin")
	output := filepath.Join(root, "backups")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return nil, nil, err
	}
	calls := filepath.Join(root, "calls")
	if err := writeHarnessSnapshotExecutable(filepath.Join(bin, "scenery"), `#!/bin/sh
printf 'scenery %s\n' "$*" >> "$CALLS"
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then output="$2"; break; fi
  shift
done
if [ -n "$output" ]; then : > "$output"; fi
`); err != nil {
		return nil, nil, err
	}
	if err := writeHarnessSnapshotExecutable(filepath.Join(bin, "rclone"), `#!/bin/sh
printf 'rclone %s\n' "$*" >> "$CALLS"
`); err != nil {
		return nil, nil, err
	}
	for _, name := range []string{"snapshot-20260101T000000Z.zip", "snapshot-20260102T000000Z.zip"} {
		if err := os.WriteFile(filepath.Join(output, name), nil, 0o600); err != nil {
			return nil, nil, err
		}
	}

	script := filepath.Join(repoRoot, "scripts", "snapshot-backup.sh")
	command := exec.CommandContext(ctx, "bash", script, "--app-root", "/tmp/app", "--output-dir", output, "--keep", "2", "--copy-to", "remote:app")
	command.Env = envWithOverrides(envpolicy.Environ(), "PATH="+bin+":/usr/bin:/bin", "CALLS="+calls)
	combined, err := command.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot backup: %w\n%s", err, combined)
	}
	entries, err := filepath.Glob(filepath.Join(output, "snapshot-*.zip"))
	if err != nil {
		return nil, nil, err
	}
	if len(entries) != 2 {
		return nil, nil, fmt.Errorf("retained snapshots = %v, want 2", entries)
	}
	if _, err := os.Stat(filepath.Join(output, "snapshot-20260101T000000Z.zip")); !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("oldest snapshot was not pruned: %v", err)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "snapshot save") || !strings.Contains(lines[1], "snapshot verify") || !strings.Contains(lines[2], "rclone copyto --checksum") {
		return nil, nil, fmt.Errorf("snapshot backup calls = %q", lines)
	}
	return map[string]any{
		"proof":              "snapshot_backup_saved_verified_replicated_and_pruned",
		"retained_snapshots": len(entries),
		"calls":              lines,
	}, nil, nil
}

func writeHarnessSnapshotExecutable(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o755)
}
