package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

const harnessStandardAuthName = "standard auth lifecycle"

func runHarnessStandardAuthStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessStandardAuthStepWithCheck(ctx, repoRoot, runHarnessStandardAuth)
}

func runHarnessStandardAuthStepWithCheck(ctx context.Context, repoRoot string, check func(context.Context, string) (map[string]any, error)) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessStandardAuthName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"}}
	var err error
	step.Summary, err = check(ctx, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), err == nil
	if err != nil {
		step.Error = err.Error()
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error, SuggestedAction: "Restore all public auth/SQL journeys and owned-resource cleanup, then rerun `go run ./scripts/verify --probe auth --summary --write`."}}
	}
	return step
}

// Every journey gets a fresh native process and database. Only the retained
// public worktree lifecycle helpers provision and retire its disposable cluster.
func runHarnessStandardAuth(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if !harnessDockerAvailable(ctx) {
		return nil, errors.New("docker is required; standard auth release proof cannot skip")
	}
	root, err := os.MkdirTemp("", "scenery-standard-auth-*")
	if err != nil {
		return nil, err
	}
	fixture, home := filepath.Join(root, "worktree"), filepath.Join(root, "state")
	restore := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(home), "SCENERY_APP_ROOT": nil, "DATABASE_URL": nil, "SCENERY_DATABASE_JSON": nil, "REPORTS_DATABASE_URL": nil, "CACHE_DATABASE_URL": nil})
	defer restore()
	summary = map[string]any{"fixture_root": root}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := cleanupHarnessWorktreePostgres(cleanup, fixture, "postgres-harness")
		summary["cleanup_ok"] = err == nil
		returnErr = errors.Join(returnErr, err)
		if returnErr == nil {
			returnErr = os.RemoveAll(root)
		}
	}()
	if err := writePostgresHarnessConfig(repoRoot, fixture); err != nil {
		return summary, err
	}
	_, database, err := provisionHarnessDatabase(ctx, repoRoot, fixture, app.Config{Name: "postgres-harness", ID: "postgres-harness"}, "cache", "reports")
	if err != nil {
		return summary, err
	}
	// The application host owns standard authentication. Its browser journeys
	// run against this empty, probe-owned database.
	manifest := filepath.Join(repoRoot, "sdk", "rust", "scenery-kernel", "Cargo.toml")
	command := commandTreeContext(ctx, "cargo", "+"+harnessRustToolchain, "test", "--locked", "--manifest-path", manifest, "browser_auth_test::", "--", "--ignored")
	command.Dir = repoRoot
	command.Env = envWithOverrides(envpolicy.Environ(), "DATABASE_URL="+database.URL)
	output, err := command.CombinedOutput()
	summary["output_tail"] = tailString(string(output), 4096)
	if err != nil {
		return summary, fmt.Errorf("host standard auth journeys: %w", err)
	}
	if !strings.Contains(string(output), "test result: ok.") || strings.Contains(string(output), " 0 passed") {
		return summary, fmt.Errorf("host standard auth journeys ran no test")
	}
	summary["proof"] = "host_email_session_and_oauth_connection_routes_on_managed_postgres"
	return summary, nil
}
