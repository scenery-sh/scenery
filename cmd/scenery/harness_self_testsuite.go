package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/testsuite"
)

const harnessTestsuiteCacheProbeName = "fresh test-binary cache probe"

type harnessTestsuiteCacheCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessTestsuiteCacheProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessTestsuiteCacheProbeStepWithCheck(ctx, repoRoot, runHarnessTestsuiteCacheProbeCheck)
}

func runHarnessTestsuiteCacheProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessTestsuiteCacheCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessTestsuiteCacheProbeName,
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
				SuggestedAction: "Fix fresh test-binary caching or execution, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessTestsuiteCacheProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-testsuite-cache-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	repoRoot := filepath.Join(root, "repo")
	cacheDir := filepath.Join(root, "cache")
	marker := filepath.Join(root, "executions")
	files := map[string]string{
		"go.mod": "module example.com/testsuiteprobe\n\ngo 1.27.0\n",
		"a/a.go": "package a\n\nfunc Value() int { return 1 }\n",
		"a/a_test.go": fmt.Sprintf(`package a

import (
	"os"
	"testing"
)

func TestFreshProbe(t *testing.T) {
	file, err := os.OpenFile(%q, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("x"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
`, marker),
		"b/b.go": "package b\n\nfunc Value() int { return 2 }\n",
	}
	for path, body := range files {
		if err := writeHarnessTestsuiteFile(filepath.Join(repoRoot, filepath.FromSlash(path)), body); err != nil {
			return nil, nil, err
		}
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "harness@example.invalid"},
		{"config", "user.name", "Scenery Harness"},
		{"add", "."},
		{"commit", "-m", "fixture"},
	} {
		if _, err := runHarnessGit(ctx, repoRoot, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	env := envpolicy.Environ()
	run := func(refresh bool) (testsuite.Result, string, error) {
		var output bytes.Buffer
		result, err := testsuite.Run(ctx, testsuite.Options{
			RepoRoot: repoRoot, CacheDir: cacheDir, RunPattern: "^TestFreshProbe$",
			PackageParallelism: 1, BuildParallelism: 1, RefreshManifest: refresh,
			RecordTimings: false, Output: &output, Env: env,
		})
		return result, output.String(), err
	}
	first, firstOutput, err := run(true)
	if err != nil {
		return nil, nil, err
	}
	second, secondOutput, err := run(false)
	if err != nil {
		return nil, nil, err
	}
	if first.ManifestHit || first.BuiltCount != 1 || first.PackageCount != 2 || first.TestPackageCount != 1 || first.TestResultCount != 1 {
		return nil, nil, fmt.Errorf("cold fresh-run result = %+v", first)
	}
	if !second.ManifestHit || second.BuiltCount != 0 || second.PackageCount != 2 || second.TestPackageCount != 1 || second.TestResultCount != 1 {
		return nil, nil, fmt.Errorf("warm fresh-run result = %+v", second)
	}
	if err := writeHarnessTestsuiteFile(filepath.Join(repoRoot, "a", "a.go"), "package a\n\nfunc Value() int { return 3 }\n"); err != nil {
		return nil, nil, err
	}
	dirty, dirtyOutput, err := run(false)
	if err != nil {
		return nil, nil, err
	}
	if dirty.ManifestHit || dirty.BuiltCount != 1 {
		return nil, nil, fmt.Errorf("dirty tracked-source result = %+v", dirty)
	}
	for _, args := range [][]string{{"add", "a/a.go"}, {"commit", "-m", "change value"}} {
		if _, err := runHarnessGit(ctx, repoRoot, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	committed, committedOutput, err := run(false)
	if err != nil {
		return nil, nil, err
	}
	if !committed.ManifestHit || committed.BuiltCount != 0 {
		return nil, nil, fmt.Errorf("committed unchanged-content result = %+v", committed)
	}
	for label, output := range map[string]string{"cold": firstOutput, "warm": secondOutput, "dirty": dirtyOutput, "committed": committedOutput} {
		if !strings.Contains(output, `"Test":"TestFreshProbe"`) || !strings.Contains(output, `"Package":"example.com/testsuiteprobe/b"`) {
			return nil, nil, fmt.Errorf("%s fresh-run JSON events are incomplete: %s", label, output)
		}
	}
	markerData, err := os.ReadFile(marker)
	if err != nil {
		return nil, nil, err
	}
	if string(markerData) != "xxxx" {
		return nil, nil, fmt.Errorf("fresh execution marker = %q, want four executions", markerData)
	}
	return map[string]any{
		"proof":                 "real_go_test_binary_cache_and_git_workspace_fingerprint_verified",
		"cold_built_binaries":   first.BuiltCount,
		"warm_built_binaries":   second.BuiltCount,
		"fresh_execution_count": len(markerData),
		"dirty_rebuilt":         !dirty.ManifestHit,
		"commit_preserved_hit":  committed.ManifestHit,
	}, nil, nil
}

func writeHarnessTestsuiteFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
