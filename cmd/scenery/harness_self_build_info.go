package main

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

const harnessBuildInfoProbeName = "trimpath build-info probe"

type harnessBuildInfoCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessBuildInfoProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessBuildInfoProbeStepWithCheck(ctx, repoRoot, runHarnessBuildInfoProbeCheck)
}

func runHarnessBuildInfoProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessBuildInfoCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessBuildInfoProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix trimpath build-info freshness detection, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessBuildInfoProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-build-info-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	if err := writeHarnessBuildInfoFile(filepath.Join(root, "go.mod"), "module scenery.sh\n\ngo 1.27.0\n"); err != nil {
		return nil, nil, err
	}
	if err := writeHarnessBuildInfoFile(filepath.Join(root, "cmd", "scenery", "main.go"), "package main\n\nfunc main() {}\n"); err != nil {
		return nil, nil, err
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "harness@example.invalid"},
		{"config", "user.name", "Scenery Harness"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		if _, err := runHarnessGit(ctx, root, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	initialRevision, err := runHarnessGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	initialRevision = strings.TrimSpace(initialRevision)
	binaryPath := filepath.Join(root, "bin", "scenery")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		return nil, nil, err
	}
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binaryPath, "./cmd/scenery")
	command.Dir = root
	command.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("build trimpath scenery probe: %w: %s", err, strings.TrimSpace(string(output)))
	}
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		return nil, nil, err
	}
	binaryRevision, ok := harnessBuildInfoSetting(info, "vcs.revision")
	if info.Main.Path != "scenery.sh" || !ok || binaryRevision != initialRevision {
		return nil, nil, fmt.Errorf("trimpath build info = module:%q revision:%q, want scenery.sh %q", info.Main.Path, binaryRevision, initialRevision)
	}
	if modified, ok := harnessBuildInfoSetting(info, "vcs.modified"); ok && modified == "true" {
		return nil, nil, fmt.Errorf("clean trimpath build is marked modified")
	}
	if err := writeHarnessBuildInfoFile(filepath.Join(root, "internal", "app", "root.go"), "package app\n"); err != nil {
		return nil, nil, err
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "change"}} {
		if _, err := runHarnessGit(ctx, root, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	changedRevision, err := runHarnessGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	changedRevision = strings.TrimSpace(changedRevision)
	if changedRevision == initialRevision || binaryRevision == changedRevision {
		return nil, nil, fmt.Errorf("old binary unexpectedly matches changed repo revision %q", changedRevision)
	}
	return map[string]any{
		"proof":            "trimpath_binary_vcs_revision_matches_clean_repo_and_rejects_changed_revision",
		"binary_revision":  binaryRevision,
		"changed_revision": changedRevision,
	}, nil, nil
}

func harnessBuildInfoSetting(info *debug.BuildInfo, key string) (string, bool) {
	if info == nil {
		return "", false
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value, true
		}
	}
	return "", false
}

func writeHarnessBuildInfoFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
