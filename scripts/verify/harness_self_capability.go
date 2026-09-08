package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
)

const harnessCapabilityAuthorityName = "capability authority"

func runHarnessCapabilityAuthorityStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessCapabilityAuthorityStepWithCheck(ctx, repoRoot, runHarnessCapabilityAuthority)
}

func runHarnessCapabilityAuthorityStepWithCheck(ctx context.Context, repoRoot string, check func(context.Context, string) (map[string]any, error)) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessCapabilityAuthorityName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"}}
	var err error
	step.Summary, err = check(ctx, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), err == nil
	if err != nil {
		step.Error = err.Error()
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error, SuggestedAction: "Restore canonical SQL supply and retained-resource recovery, then rerun the repository release verifier."}}
	}
	return step
}

// These journeys cross real compiler, application, PostgreSQL and snapshot
// boundaries. All sources, processes and SQL resources belong to this probe.
func runHarnessCapabilityAuthority(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-capability-authority-*")
	if err != nil {
		return nil, err
	}
	home := filepath.Join(root, "state")
	env := envWithOverrides(harnessAppEnv(home), "SCENERY_DATABASE_JSON=", "SCENERY_DURABLE_ENDPOINT=", "SCENERY_ROLE=", "INBOX_DATABASE_URL=")
	segments := &harnessNativeContractProbeSegments{}
	summary = map[string]any{"fixture_root": root, "candidate": harnessLocalSceneryBinaryPath(repoRoot), "assertions": segments.entries}
	summary["candidate_sha256"], err = worktreeProbeFileSHA(harnessLocalSceneryBinaryPath(repoRoot))
	if err != nil {
		return summary, err
	}
	defer func() { summary["assertions"] = segments.entries }()
	roots := []string{filepath.Join(root, "basic"), filepath.Join(root, "webhook"), filepath.Join(root, "instances")}
	restore := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(home), "DATABASE_URL": nil})
	defer restore()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		var cleanupErr error
		for _, appRoot := range roots {
			if _, err := os.Stat(appRoot); os.IsNotExist(err) {
				continue
			}
			_, downErr := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json")
			cleanupErr = errors.Join(cleanupErr, downErr)
			if downErr == nil {
				cleanupErr = errors.Join(cleanupErr, cleanupHarnessWorktreePostgres(cleanup, appRoot, ""))
			}
		}
		returnErr = errors.Join(returnErr, cleanupErr)
		if returnErr == nil {
			returnErr = os.RemoveAll(root)
		}
		summary["cleanup_ok"] = cleanupErr == nil
	}()
	cli := func(appRoot string, args ...string) ([]byte, error) {
		return runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, args...)
	}
	if err := segments.run("A6 no-SQL source-only generate/check/test/harness/inspect/up/ps/db-list/down without PostgreSQL", func() error {
		appRoot := roots[0]
		if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
			return err
		}
		if err := verifyHarnessCapabilityAppCommands(ctx, repoRoot, appRoot, env); err != nil {
			return err
		}
		requirements, err := readHarnessSQLRequirements(cli, appRoot)
		if err != nil || len(requirements) != 0 {
			return fmt.Errorf("no-SQL requirements=%+v: %w", requirements, err)
		}
		paths, err := localagent.PathsForWorktree(home, appRoot)
		if err != nil {
			return err
		}
		if _, err := os.Stat(paths.Directory); !os.IsNotExist(err) {
			return fmt.Errorf("read-only app commands allocated worktree state: %v", err)
		}
		if _, err := cli(appRoot, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
			return err
		}
		if _, err := cli(appRoot, "ps", "-o", "json"); err != nil {
			return err
		}
		output, err := cli(appRoot, "db", "list", "-o", "json")
		var listing struct {
			Database json.RawMessage `json:"database"`
		}
		if err != nil || decodeCLIJSON(output, &listing) != nil || string(listing.Database) != "null" {
			return fmt.Errorf("no-SQL database absence not reported: %s: %w", output, err)
		}
		record, err := paths.LoadRecord("")
		if err != nil || record.Postgres != nil {
			return fmt.Errorf("no-SQL startup allocated PostgreSQL: %w", err)
		}
		_, err = cli(appRoot, "down", "-o", "json")
		return err
	}); err != nil {
		return summary, err
	}
	if !harnessDockerAvailable(ctx) {
		return summary, errors.New("docker is unavailable; mandatory managed SQL and external ownership assertions did not run")
	}
	if err := segments.run("A8 standard-auth-only registration supplies framework SQL without Google or a data_source", func() error {
		return verifyHarnessAuthOnlyCapability(ctx, repoRoot, roots[0], env)
	}); err != nil {
		return summary, err
	}
	appRoot := roots[1]
	if err := copyHarnessWebhookFixture(repoRoot, appRoot); err != nil {
		return summary, err
	}
	if err := segments.run("A8 external lifecycle without endpoint fails before allocation", func() error {
		output, err := cli(appRoot, "db", "server", "start", "-o", "json")
		if err == nil || !strings.Contains(string(output), "does not authorize managed provisioning") {
			return fmt.Errorf("external declaration allowed implicit allocation: %s: %w", output, err)
		}
		paths, err := localagent.PathsForWorktree(home, appRoot)
		if err != nil {
			return err
		}
		if _, err := os.Stat(paths.Record); !os.IsNotExist(err) {
			return fmt.Errorf("rejected supply created allocation record: %v", err)
		}
		return nil
	}); err != nil {
		return summary, err
	}
	if err := replaceHarnessSource(appRoot, "app.scn", `lifecycle            = "external"`, `lifecycle            = "managed"`); err != nil {
		return summary, err
	}
	// Exercise the retained no-name db.Get API in the real setup process with
	// application plus framework bindings supplied by ordinary managed startup.
	if err := replaceHarnessSource(appRoot, "cmd/schema/main.go", `db.Get(ctx, "inbox")`, `db.Get(ctx)`); err != nil {
		return summary, err
	}
	if err := segments.run("A7/A8 managed SQL, auth and durable requirements feed ordinary setup and typed HTTP/client behavior", func() error {
		if err := verifyHarnessCapabilityAppCommands(ctx, repoRoot, appRoot, env); err != nil {
			return err
		}
		requirements, err := readHarnessSQLRequirements(cli, appRoot)
		if err != nil {
			return err
		}
		if len(requirements) != 3 || len(requirements.Bindings(false)) != 2 || len(requirements.Bindings(true)) != 2 {
			return fmt.Errorf("webhook must retain data, auth and durable identities: %+v", requirements)
		}
		output, err := cli(appRoot, "up", "--detach", "--wait", "ready", "-o", "json")
		var runtime detachedDevResult
		if err != nil || decodeCLIJSON(output, &runtime) != nil {
			return fmt.Errorf("managed webhook startup: %s: %w", output, err)
		}
		if _, err := cli(appRoot, "ps", "-o", "json"); err != nil {
			return err
		}
		if _, err := cli(appRoot, "db", "list", "-o", "json"); err != nil {
			return err
		}
		return verifyHarnessWebhookHTTP(ctx, appRoot, worktreeProbeAPI(runtime), env)
	}); err != nil {
		return summary, err
	}
	if err := segments.run("A10 invalid and removed source preserve snapshot, stopped-server recovery and cleanup authority", func() error {
		return verifyHarnessCapabilityRecovery(ctx, repoRoot, appRoot, root, env)
	}); err != nil {
		return summary, err
	}
	if err := segments.run("A8 multiple module instances preserve shared source and distinct logical schemas", func() error {
		return verifyHarnessCapabilityInstances(ctx, repoRoot, roots[2], home, env)
	}); err != nil {
		return summary, err
	}
	if err := segments.run("A9 external DATABASE_URL standalone API and worker retain typed SQL/auth/durable behavior", func() error {
		cmd := commandTreeContext(ctx, "bash", filepath.Join(repoRoot, "examples/webhook-inbox/verify.sh"), harnessLocalSceneryBinaryPath(repoRoot))
		cmd.Dir, cmd.Env = repoRoot, env
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "PASS durable admission") {
			return fmt.Errorf("external webhook proof: %w: %s", err, tailString(string(output), 8192))
		}
		summary["external_proof"] = strings.TrimSpace(string(output))
		return nil
	}); err != nil {
		return summary, err
	}
	return summary, nil
}

func readHarnessSQLRequirements(cli func(string, ...string) ([]byte, error), root string) (compiler.SQLRequirements, error) {
	output, err := cli(root, "inspect", "app", "-o", "json")
	if err != nil {
		return nil, err
	}
	var app struct {
		SQLRequirements compiler.SQLRequirements `json:"sql_requirements"`
	}
	if err := decodeCLIJSON(output, &app); err != nil {
		return nil, err
	}
	if app.SQLRequirements == nil {
		return nil, errors.New("current inspection omitted sql_requirements")
	}
	return app.SQLRequirements, nil
}

func verifyHarnessCapabilityAppCommands(ctx context.Context, repoRoot, appRoot string, env []string) error {
	if _, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "generate", "-o", "json"); err != nil {
		return err
	}
	// The replacement framework can add transitive imports relative to the
	// committed fixture lock. Refresh only this owned source copy after generation.
	tidy := commandTreeContext(ctx, "go", "mod", "tidy")
	tidy.Dir, tidy.Env = appRoot, env
	if output, err := tidy.CombinedOutput(); err != nil {
		return fmt.Errorf("source-only application dependency preparation: %w: %s", err, tailString(string(output), 8192))
	}
	if _, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "check", "-o", "json"); err != nil {
		return err
	}
	cmd := commandTreeContext(ctx, "go", "test", "./...")
	cmd.Dir, cmd.Env = appRoot, env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("source-only application tests: %w: %s", err, tailString(string(output), 8192))
	}
	_, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "harness", "-o", "json", "--write")
	return err
}
