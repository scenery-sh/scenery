package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/envpolicy"
)

var runHarnessNeonLocalLifecycleCheckFunc = runHarnessNeonLocalLifecycleCheck

func runHarnessNeonLocalLifecycleStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "neon local branch lifecycle",
		Command: []string{"onlava", "harness", "self", "internal:neon-local-lifecycle", repoRoot},
	}
	summary, diagnostics, err := runHarnessNeonLocalLifecycleCheckFunc(ctx)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = summary
	step.Diagnostics = diagnostics
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the local Neon branch pin and lease lifecycle, then rerun `onlava harness self --json`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessNeonSelfhostStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "neon selfhost real lifecycle",
		Command: []string{"onlava", "harness", "self", "--with-neon-selfhost", "--repo-root", repoRoot},
	}
	summary, diagnostics, err := runHarnessNeonSelfhostCheck(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = summary
	step.Diagnostics = diagnostics
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the real self-hosted Neon dev-cell lifecycle, then rerun `onlava harness self --json --write --with-neon-selfhost`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessNeonSelfhostCheck(parent context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Minute)
	defer cancel()

	var diagnostics []checkDiagnostic
	check := func(ok bool, message string) {
		if ok {
			return
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "neon selfhost real lifecycle",
			Severity:        "error",
			Message:         message,
			SuggestedAction: "Inspect `onlava db neon status --json`, Docker logs, and branch status, then rerun `onlava harness self --json --write --with-neon-selfhost`.",
		})
	}
	requireTool := func(name string) error {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("%s is required for --with-neon-selfhost: %w", name, err)
		}
		return nil
	}
	if err := requireTool("docker"); err != nil {
		return nil, diagnostics, err
	}
	if err := requireTool("psql"); err != nil {
		return nil, diagnostics, err
	}
	if _, err := runDockerProbe(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		return nil, diagnostics, fmt.Errorf("docker daemon is required for --with-neon-selfhost: %w", err)
	}

	agentHome := filepath.Join(os.TempDir(), "onlava-harness-neon-selfhost-"+harnessRandomLabel())
	restoreEnv := patchEnv(map[string]*string{
		"ONLAVA_AGENT_HOME":          stringPtr(agentHome),
		neonSelfhostBranchDriverEnv:  nil,
		localPostgresBranchDriverEnv: nil,
		devElectricUpstreamEnv:       nil,
	})
	defer restoreEnv()
	defer os.RemoveAll(agentHome)
	defer func() {
		_ = runDBNeonCommand(context.Background(), &bytes.Buffer{}, []string{"uninstall", "--destroy-data", "--json"})
	}()

	root := filepath.Join(os.TempDir(), "onlava-harness-neon-selfhost-app-"+harnessRandomLabel(), "demo")
	defer os.RemoveAll(filepath.Dir(root))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, diagnostics, err
	}
	if err := os.WriteFile(filepath.Join(root, ".onlava.json"), []byte(`{
		"name": "neon-selfhost-harness",
		"id": "neon-selfhost-harness",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"isolation": "branch",
					"project": "neon-selfhost-harness",
					"database": "neon_selfhost_harness",
					"branch_name_template": "{app}/{git_branch}"
				},
				"electric": {
					"kind": "electric"
				}
			}
		}
	}`), 0o644); err != nil {
		return nil, diagnostics, err
	}
	if err := runGitCommand(ctx, "-C", root, "init", "-b", "main"); err != nil {
		return nil, diagnostics, err
	}
	if err := runGitCommand(ctx, "-C", root, "config", "user.email", "harness@example.com"); err != nil {
		return nil, diagnostics, err
	}
	if err := runGitCommand(ctx, "-C", root, "config", "user.name", "Onlava Harness"); err != nil {
		return nil, diagnostics, err
	}
	if err := runGitCommand(ctx, "-C", root, "add", ".onlava.json"); err != nil {
		return nil, diagnostics, err
	}
	if err := runGitCommand(ctx, "-C", root, "commit", "-m", "initial"); err != nil {
		return nil, diagnostics, err
	}

	if err := runDBNeonCommand(ctx, &bytes.Buffer{}, []string{"install", "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var startOut bytes.Buffer
	if err := runDBNeonCommand(ctx, &startOut, []string{"start", "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var startStatus dbNeonStatusResult
	if err := json.Unmarshal(startOut.Bytes(), &startStatus); err != nil {
		return nil, diagnostics, fmt.Errorf("decode Neon start JSON: %w: %s", err, startOut.String())
	}
	check(startStatus.Driver != nil && startStatus.Driver.Kind == "builtin" && startStatus.Driver.Tool == neonSelfhostDriverTool, "install/start must resolve the built-in neon-selfhost driver")

	createdA, err := harnessCreateNeonWorktree(ctx, root, "selfhost-a")
	if err != nil {
		return nil, diagnostics, err
	}
	createdB, err := harnessCreateNeonWorktree(ctx, root, "selfhost-b")
	if err != nil {
		return nil, diagnostics, err
	}
	statusA, err := harnessNeonBranchStatus(ctx, createdA.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	statusB, err := harnessNeonBranchStatus(ctx, createdB.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	check(statusA.BackendStatus == "ready", "worktree A must get a ready Neon selfhost branch")
	check(statusB.BackendStatus == "ready", "worktree B must get a ready Neon selfhost branch")
	if statusA.Connection != nil && statusB.Connection != nil {
		check(statusA.Connection.Port != statusB.Connection.Port, "parallel Neon selfhost branches must use distinct compute ports")
	}
	if err := harnessManagedPSQL(ctx, createdA.Path, "-v", "ON_ERROR_STOP=1", "-c", "create table if not exists onlava_harness_probe(id integer primary key); insert into onlava_harness_probe(id) values (1) on conflict do nothing"); err != nil {
		return nil, diagnostics, fmt.Errorf("write probe data to branch A: %w", err)
	}
	branchBProbe, err := harnessManagedPSQLOutput(ctx, createdB.Path, "-tAc", "select to_regclass('public.onlava_harness_probe') is null")
	if err != nil {
		return nil, diagnostics, fmt.Errorf("read branch B isolation probe: %w", err)
	}
	check(strings.TrimSpace(branchBProbe) == "t", "branch B must not observe table created only on branch A")

	_, cfgA, err := discoverConfiguredApp(createdA.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	managedEnvA, _, connectionA, err := neonManagedPostgresEnv(ctx, createdA.Path, cfgA, &localagent.Session{SessionID: "selfhost-a", BaseAppID: "neon-selfhost-harness", Branch: "selfhost-a"})
	if err != nil {
		return nil, diagnostics, err
	}
	electricURLA, err := managedElectricDatabaseURL(ctx, createdA.Path, cfgA, &localagent.Session{SessionID: "selfhost-a"}, &managedElectricPlan{ServiceName: "electric"}, nil, nil)
	if err != nil {
		return nil, diagnostics, err
	}
	check(envValueFromList(managedEnvA, appDatabaseURLEnv) == connectionA.DatabaseURL, "managed env must expose the ready selfhost branch DatabaseURL")
	check(electricURLA == connectionA.DatabaseURL, "managed Electric must resolve the ready selfhost branch DatabaseURL")

	if err := harnessManagedPSQL(ctx, createdA.Path, "-v", "ON_ERROR_STOP=1", "-c", "create table if not exists onlava_harness_reset(id integer primary key)"); err != nil {
		return nil, diagnostics, fmt.Errorf("create reset probe table: %w", err)
	}
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"reset", "--yes", "--app-root", createdA.Path}); err != nil {
		return nil, diagnostics, fmt.Errorf("reset selfhost branch: %w", err)
	}
	resetStatus, err := harnessNeonBranchStatus(ctx, createdA.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	check(resetStatus.BackendStatus == "ready", "reset selfhost branch must return to ready status")
	resetProbe, err := harnessManagedPSQLOutput(ctx, createdA.Path, "-tAc", "select to_regclass('public.onlava_harness_reset') is null")
	if err != nil {
		return nil, diagnostics, fmt.Errorf("read reset probe table: %w", err)
	}
	check(strings.TrimSpace(resetProbe) == "t", "reset selfhost branch must replace the branch timeline")

	if err := harnessManagedPSQL(ctx, createdA.Path, "-v", "ON_ERROR_STOP=1", "-c", "create table if not exists onlava_harness_restore_keep(id integer primary key)"); err != nil {
		return nil, diagnostics, fmt.Errorf("create restore baseline table: %w", err)
	}
	restoreLSN, err := harnessManagedPSQLOutput(ctx, createdA.Path, "-tAc", "select pg_current_wal_lsn()")
	if err != nil {
		return nil, diagnostics, fmt.Errorf("read restore LSN: %w", err)
	}
	restoreLSN = strings.TrimSpace(restoreLSN)
	if err := harnessManagedPSQL(ctx, createdA.Path, "-v", "ON_ERROR_STOP=1", "-c", "create table if not exists onlava_harness_restore_after(id integer primary key)"); err != nil {
		return nil, diagnostics, fmt.Errorf("create post-restore probe table: %w", err)
	}
	var restoreOut bytes.Buffer
	if err := runDBBranchCommand(ctx, &restoreOut, []string{"restore", "--at", restoreLSN, "--yes", "--app-root", createdA.Path, "--json"}); err != nil {
		return nil, diagnostics, fmt.Errorf("restore selfhost branch: %w", err)
	}
	if !strings.Contains(restoreOut.String(), `"schema_version": "onlava.db.branch.restore.v1"`) {
		check(false, "restore selfhost branch must emit restore JSON")
	}
	restoreStatus, err := harnessNeonBranchStatus(ctx, createdA.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	check(restoreStatus.BackendStatus == "ready", "restore selfhost branch must return to ready status")
	restoreAfterProbe, err := harnessManagedPSQLOutput(ctx, createdA.Path, "-tAc", "select to_regclass('public.onlava_harness_restore_after') is null")
	if err != nil {
		return nil, diagnostics, fmt.Errorf("read post-restore probe table: %w", err)
	}
	check(strings.TrimSpace(restoreAfterProbe) == "t", "restore selfhost branch must replace the branch timeline at the requested LSN")

	var diffOut bytes.Buffer
	if createdB.DBPin != nil {
		if err := runDBBranchCommand(ctx, &diffOut, []string{"diff", createdB.DBPin.Branch, "--app-root", createdA.Path, "--json"}); err != nil {
			return nil, diagnostics, fmt.Errorf("diff selfhost branches: %w", err)
		}
		check(strings.Contains(diffOut.String(), `"schema_version": "onlava.db.branch.diff.v1"`) && strings.Contains(diffOut.String(), "onlava_harness_restore"), "diff selfhost branches must emit schema diff JSON")
		if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"delete", createdB.DBPin.Branch, "--app-root", createdA.Path, "--force"}); err != nil {
			return nil, diagnostics, fmt.Errorf("delete selfhost branch: %w", err)
		}
	}

	summary := map[string]any{
		"worktrees":          2,
		"dev_cell_lifecycle": true,
		"managed_driver":     true,
		"branches_ready":     statusA.BackendStatus == "ready" && statusB.BackendStatus == "ready",
		"branch_isolation":   strings.TrimSpace(branchBProbe) == "t",
		"electric_db_url":    electricURLA == connectionA.DatabaseURL,
		"reset_restore":      resetStatus.BackendStatus == "ready" && restoreStatus.BackendStatus == "ready",
		"schema_diff":        strings.Contains(diffOut.String(), `"schema_version": "onlava.db.branch.diff.v1"`),
		"delete":             createdB.DBPin != nil,
		"diagnostics":        len(diagnostics),
	}
	if hasErrorDiagnostics(diagnostics) {
		return summary, diagnostics, fmt.Errorf("Neon selfhost real lifecycle check failed")
	}
	return summary, diagnostics, nil
}

func runHarnessNeonLocalLifecycleCheck(parent context.Context) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	agentHome := filepath.Join(os.TempDir(), "onlava-harness-neon-"+harnessRandomLabel())
	defer os.RemoveAll(agentHome)
	branchDriverLog, branchDriver, err := installHarnessFakeDriver(agentHome)
	if err != nil {
		return nil, nil, err
	}
	restoreEnv := patchEnv(map[string]*string{
		"ONLAVA_AGENT_HOME":          stringPtr(agentHome),
		neonSelfhostBranchDriverEnv:  stringPtr(branchDriver),
		localPostgresBranchDriverEnv: nil,
		devElectricUpstreamEnv:       nil,
	})
	defer restoreEnv()
	dockerCallLog, restoreDocker, err := installHarnessFakeNeonDocker(agentHome)
	if err != nil {
		return nil, nil, err
	}
	defer restoreDocker()

	root := filepath.Join(os.TempDir(), "onlava-harness-neon-app-"+harnessRandomLabel(), "demo")
	defer os.RemoveAll(filepath.Dir(root))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(root, ".onlava.json"), []byte(`{
		"name": "neon-harness",
		"id": "neon-harness",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"isolation": "branch",
					"project": "neon-harness",
					"branch_name_template": "{app}/{git_branch}"
				}
			}
		}
	}`), 0o644); err != nil {
		return nil, nil, err
	}
	if err := runGitCommand(ctx, "-C", root, "init", "-b", "main"); err != nil {
		return nil, nil, err
	}
	if err := runGitCommand(ctx, "-C", root, "config", "user.email", "harness@example.com"); err != nil {
		return nil, nil, err
	}
	if err := runGitCommand(ctx, "-C", root, "config", "user.name", "Onlava Harness"); err != nil {
		return nil, nil, err
	}
	if err := runGitCommand(ctx, "-C", root, "add", ".onlava.json"); err != nil {
		return nil, nil, err
	}
	if err := runGitCommand(ctx, "-C", root, "commit", "-m", "initial"); err != nil {
		return nil, nil, err
	}

	var diagnostics []checkDiagnostic
	check := func(ok bool, message string) {
		if ok {
			return
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "neon local branch lifecycle",
			Severity:        "error",
			Message:         message,
			SuggestedAction: "Fix the local Neon branch pin and lease lifecycle, then rerun `onlava harness self --json`.",
		})
	}

	if err := runDBNeonCommand(ctx, &bytes.Buffer{}, []string{"install", "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var startOut bytes.Buffer
	if err := runDBNeonCommand(ctx, &startOut, []string{"start", "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var startStatus dbNeonStatusResult
	if err := json.Unmarshal(startOut.Bytes(), &startStatus); err != nil {
		return nil, diagnostics, fmt.Errorf("decode Neon start JSON: %w: %s", err, startOut.String())
	}
	check(strings.Contains(startStatus.Message, "Started generated Neon dev-cell project"), "neon start must report the generated dev-cell lifecycle message")
	var stopOut bytes.Buffer
	if err := runDBNeonCommand(ctx, &stopOut, []string{"stop", "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var stopStatus dbNeonStatusResult
	if err := json.Unmarshal(stopOut.Bytes(), &stopStatus); err != nil {
		return nil, diagnostics, fmt.Errorf("decode Neon stop JSON: %w: %s", err, stopOut.String())
	}
	check(strings.Contains(stopStatus.Message, "Stopped generated Neon dev-cell project"), "neon stop must report the generated dev-cell lifecycle message")
	dockerCalls, err := os.ReadFile(dockerCallLog)
	if err != nil {
		return nil, diagnostics, err
	}
	check(strings.Contains(string(dockerCalls), "compose -f ") && strings.Contains(string(dockerCalls), " -p onlava-neon up -d"), "neon start must use the generated Compose file and stable onlava-neon project")
	check(strings.Contains(string(dockerCalls), "compose -f ") && strings.Contains(string(dockerCalls), " -p onlava-neon stop"), "neon stop must use the generated Compose file and stable onlava-neon project")

	createdA, err := harnessCreateNeonWorktree(ctx, root, "agent-a")
	if err != nil {
		return nil, diagnostics, err
	}
	createdB, err := harnessCreateNeonWorktree(ctx, root, "agent-b")
	if err != nil {
		return nil, diagnostics, err
	}
	check(createdA.DBPin != nil && createdB.DBPin != nil, "worktree create must write Neon branch pins")
	if createdA.DBPin != nil && createdB.DBPin != nil {
		check(createdA.DBPin.Branch != createdB.DBPin.Branch, "parallel worktrees must use distinct Neon branch names")
		check(createdA.DBPin.BranchID != createdB.DBPin.BranchID, "parallel worktrees must use distinct Neon branch IDs")
		check(createdA.DBPin.WorktreeRoot != createdB.DBPin.WorktreeRoot, "parallel worktree pins must record distinct roots")
	}

	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		return nil, diagnostics, err
	}
	check(len(registry.Leases) == 2, "Neon branch registry must record both worktree leases")

	if createdA.DBPin != nil {
		if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"expire", createdA.DBPin.Branch, "--after", "-1h", "--app-root", createdA.Path, "--json"}); err != nil {
			return nil, diagnostics, err
		}
	}
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"prune", "--app-root", createdB.Path, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		return nil, diagnostics, err
	}
	check(!harnessRegistryHasBranch(registry, createdA.DBPin), "prune must remove an expired non-current Neon lease")
	check(harnessRegistryHasBranch(registry, createdB.DBPin), "prune must preserve the current Neon lease")

	if createdB.DBPin != nil {
		message, err := dropSessionManagedDatabase(ctx, createdB.Path, localagent.Session{SessionID: "agent-b"})
		if err != nil {
			return nil, diagnostics, err
		}
		check(strings.Contains(message, createdB.DBPin.Branch), "down --db cleanup must report the removed local Neon branch lease")
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		return nil, diagnostics, err
	}
	check(!harnessRegistryHasBranch(registry, createdB.DBPin), "down --db cleanup must remove only the current local Neon lease")
	removedPin, err := removeNeonWorktreeDBPinIfConfigured(createdB.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	check(removedPin, "down --state cleanup must remove the local Neon worktree pin")

	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"checkout", "main", "--app-root", createdA.Path, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"reset", "--yes", "--app-root", createdA.Path}); err == nil || !strings.Contains(err.Error(), "protected parent branch") {
		check(false, "reset must refuse the protected parent branch")
	}
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"delete", "main", "--force", "--app-root", createdA.Path}); err == nil || !strings.Contains(err.Error(), "protected parent branch") {
		check(false, "delete must refuse the protected parent branch")
	}
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"checkout", "feature/current", "--app-root", createdA.Path, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"delete", "feature/current", "--app-root", createdA.Path}); err == nil || !strings.Contains(err.Error(), "without --force") {
		check(false, "delete must refuse the current branch without --force")
	}
	var readyStatusOut bytes.Buffer
	if err := runDBBranchCommand(ctx, &readyStatusOut, []string{"status", "--app-root", createdA.Path, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var readyStatus dbBranchStatusResult
	if err := json.Unmarshal(readyStatusOut.Bytes(), &readyStatus); err != nil {
		return nil, diagnostics, fmt.Errorf("decode ready branch status JSON: %w: %s", err, readyStatusOut.String())
	}
	check(readyStatus.BackendStatus == "ready", "configured local-postgres-branch driver must mark checkout backend_status ready")
	check(readyStatus.Connection != nil && readyStatus.Connection.Source == "fake-driver", "ready branch status must expose redacted driver endpoint metadata")
	_, cfg, err := discoverConfiguredApp(createdA.Path)
	if err != nil {
		return nil, diagnostics, err
	}
	managedEnv, _, connection, err := neonManagedPostgresEnv(ctx, createdA.Path, cfg, &localagent.Session{SessionID: "agent-a", BaseAppID: "neon-harness", Branch: "agent-a"})
	if err != nil {
		return nil, diagnostics, err
	}
	check(envValueFromList(managedEnv, appDatabaseURLEnv) == connection.DatabaseURL, "managed Neon env must expose DatabaseURL from the ready branch endpoint")
	check(envValueFromList(managedEnv, "ONLAVA_MANAGED_DATABASE_URL") == connection.DatabaseURL, "managed Neon env must expose ONLAVA_MANAGED_DATABASE_URL from the ready branch endpoint")
	check(envValueFromList(managedEnv, legacyDatabaseURLEnv) == "", "managed Neon env must not inject legacy DATABASE_URL")
	electricURL, err := managedElectricDatabaseURL(ctx, createdA.Path, cfg, &localagent.Session{SessionID: "agent-a"}, &managedElectricPlan{ServiceName: "electric"}, nil, nil)
	if err != nil {
		return nil, diagnostics, err
	}
	check(electricURL == connection.DatabaseURL, "managed Electric must resolve the same ready Neon branch DatabaseURL")
	if err := runDBBranchCommand(ctx, &bytes.Buffer{}, []string{"delete", "feature/current", "--app-root", createdA.Path, "--force"}); err != nil {
		return nil, diagnostics, err
	}
	branchDriverCalls, err := os.ReadFile(branchDriverLog)
	if err != nil {
		return nil, diagnostics, err
	}
	check(strings.Contains(string(branchDriverCalls), "ensure") && strings.Contains(string(branchDriverCalls), "--branch feature/current"), "configured local-postgres-branch driver must receive ensure calls for checked-out branches")
	check(strings.Contains(string(branchDriverCalls), "delete") && strings.Contains(string(branchDriverCalls), "--branch feature/current"), "configured local-postgres-branch driver must receive ready branch delete calls")

	summary := map[string]any{
		"worktrees":          2,
		"leases":             len(registry.Leases),
		"dev_cell_lifecycle": true,
		"diagnostics":        len(diagnostics),
		"backend_required":   true,
		"branch_driver":      true,
	}
	if hasErrorDiagnostics(diagnostics) {
		return summary, diagnostics, fmt.Errorf("Neon local branch lifecycle check failed")
	}
	return summary, diagnostics, nil
}

func harnessCreateNeonWorktree(ctx context.Context, appRoot, name string) (worktreeCreateResult, error) {
	var out bytes.Buffer
	if err := runWorktreeCommand(ctx, &out, []string{"create", name, "--from", "main", "--app-root", appRoot, "--json"}); err != nil {
		return worktreeCreateResult{}, err
	}
	var result worktreeCreateResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return worktreeCreateResult{}, fmt.Errorf("decode worktree create JSON: %w: %s", err, out.String())
	}
	return result, nil
}

func harnessNeonBranchStatus(ctx context.Context, appRoot string) (dbBranchStatusResult, error) {
	var out bytes.Buffer
	if err := runDBBranchCommand(ctx, &out, []string{"status", "--app-root", appRoot, "--json"}); err != nil {
		return dbBranchStatusResult{}, err
	}
	var status dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		return dbBranchStatusResult{}, fmt.Errorf("decode branch status JSON: %w: %s", err, out.String())
	}
	return status, nil
}

func harnessManagedPSQL(ctx context.Context, appRoot string, args ...string) error {
	_, err := harnessManagedPSQLOutput(ctx, appRoot, args...)
	return err
}

func harnessManagedPSQLOutput(ctx context.Context, appRoot string, args ...string) (string, error) {
	_, cfg, err := discoverConfiguredApp(appRoot)
	if err != nil {
		return "", err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return "", err
	}
	invocation, err := buildPSQLInvocationForConfig(ctx, appRoot, cfg, baseEnv, psqlOptions{UseManaged: true, Args: args})
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, invocation.Program, invocation.Args...)
	cmd.Dir = invocation.Dir
	cmd.Env = invocation.Env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", invocation.Program, strings.Join(invocation.Args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func harnessRegistryHasBranch(registry neonBranchRegistry, pin *worktreeDBPin) bool {
	if pin == nil {
		return false
	}
	for _, lease := range registry.Leases {
		if sameNeonLease(lease.Pin, *pin) {
			return true
		}
	}
	return false
}

func installHarnessFakeDriver(root string) (string, string, error) {
	bin := filepath.Join(root, "fake-driver-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", "", err
	}
	callLog := filepath.Join(bin, "fake-driver.calls.log")
	driver := filepath.Join(bin, "fake-driver")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %s
case "$1" in
  ensure|reset|restore)
    printf '{"status":"ready","message":"harness driver ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"neon_harness","role":"cloud_admin","sslmode":"disable","source":"fake-driver"}}\n'
    exit 0
    ;;
  delete)
    printf '{"status":"deleted","message":"harness driver deleted"}\n'
    exit 0
    ;;
esac
echo "unexpected local-postgres-branch driver $*" >&2
exit 1
`, shellQuote(callLog))
	if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
		return "", "", err
	}
	return callLog, driver, nil
}

func installHarnessFakeNeonDocker(root string) (string, func(), error) {
	bin := filepath.Join(root, "fake-docker")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", nil, err
	}
	callLog := filepath.Join(bin, "calls.log")
	statePath := filepath.Join(bin, "state")
	fakeDocker := filepath.Join(bin, "docker")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %s
if [ "$1" = "version" ]; then
  echo "29.0.0"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  echo "[]"
  exit 0
fi
if [ "$1" = "ps" ]; then
  state="not-started"
  if [ -f %s ]; then
    state="$(cat %s)"
  fi
  if [ "$state" = "started" ]; then
    printf 'onlava-neon-storage-broker\tUp 2 minutes\n'
  elif [ "$state" = "stopped" ]; then
    printf 'onlava-neon-storage-broker\tExited (0) 1 second ago\n'
  fi
  exit 0
fi
if [ "$1" = "compose" ]; then
  if [ "$2" != "-f" ] || [ "$4" != "-p" ] || [ "$5" != "onlava-neon" ]; then
    echo "unexpected compose args: $*" >&2
    exit 2
  fi
  if [ "$6" = "up" ] && [ "$7" = "-d" ]; then
    echo started > %s
    exit 0
  fi
  if [ "$6" = "stop" ]; then
    echo stopped > %s
    exit 0
  fi
fi
echo "unexpected docker $*" >&2
exit 1
`, shellQuote(callLog), shellQuote(statePath), shellQuote(statePath), shellQuote(statePath), shellQuote(statePath))
	if err := os.WriteFile(fakeDocker, []byte(script), 0o755); err != nil {
		return "", nil, err
	}
	previousDockerCommand := neonDockerCommand
	neonDockerCommand = fakeDocker
	restore := func() {
		neonDockerCommand = previousDockerCommand
	}
	return callLog, restore, nil
}
