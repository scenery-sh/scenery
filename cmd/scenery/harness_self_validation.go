package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/validation"
)

const harnessValidationGitProbeName = "validation changed-files Git probe"

type harnessValidationGitCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessValidationGitProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessValidationGitProbeStepWithCheck(ctx, repoRoot, runHarnessValidationGitProbeCheck)
}

func runHarnessValidationGitProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessValidationGitCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessValidationGitProbeName,
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
				SuggestedAction: "Fix changed-file discovery across the real Git boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessValidationGitProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-validation-git-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	appRoot := filepath.Join(root, "app")
	if err := writeHarnessValidationFile(filepath.Join(appRoot, "src", "main.go"), "package main\n"); err != nil {
		return nil, nil, err
	}
	if err := writeHarnessValidationFile(filepath.Join(root, "other", "main.go"), "package main\n"); err != nil {
		return nil, nil, err
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "harness@example.invalid"},
		{"config", "user.name", "Scenery Harness"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		if _, err := runHarnessGit(ctx, root, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	base, err := runHarnessGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	if err := writeHarnessValidationFile(filepath.Join(appRoot, "src", "main.go"), "package main\nconst changed = true\n"); err != nil {
		return nil, nil, err
	}
	if err := writeHarnessValidationFile(filepath.Join(root, "other", "main.go"), "package main\nconst changed = true\n"); err != nil {
		return nil, nil, err
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "change"}} {
		if _, err := runHarnessGit(ctx, root, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	files, err := validation.CollectChangedFiles(ctx, appRoot, strings.TrimSpace(base))
	if err != nil {
		return nil, nil, err
	}
	if len(files) != 1 || files[0] != "src/main.go" {
		return nil, nil, fmt.Errorf("app-relative changed files = %v, want [src/main.go]", files)
	}
	return map[string]any{
		"proof":         "real_git_history_filtered_to_app_relative_paths",
		"changed_files": files,
	}, nil, nil
}

func writeHarnessValidationFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
