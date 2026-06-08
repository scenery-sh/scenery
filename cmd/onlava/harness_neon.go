package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
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

func runHarnessNeonLocalLifecycleCheck(parent context.Context) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	agentHome := filepath.Join(os.TempDir(), "onlava-harness-neon-"+harnessRandomLabel())
	defer os.RemoveAll(agentHome)
	branchDriverLog, branchDriver, err := installHarnessFakeNeonBranchDriver(agentHome)
	if err != nil {
		return nil, nil, err
	}
	restoreEnv := patchEnv(map[string]*string{
		"ONLAVA_AGENT_HOME":    stringPtr(agentHome),
		neonBranchDriverEnv:    stringPtr(branchDriver),
		devElectricUpstreamEnv: nil,
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
	check(readyStatus.BackendStatus == "ready", "configured Neon branch driver must mark checkout backend_status ready")
	check(readyStatus.Connection != nil && readyStatus.Connection.Source == "harness-driver", "ready branch status must expose redacted driver endpoint metadata")
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
	check(strings.Contains(string(branchDriverCalls), "ensure") && strings.Contains(string(branchDriverCalls), "--branch feature/current"), "configured branch driver must receive ensure calls for checked-out branches")
	check(strings.Contains(string(branchDriverCalls), "delete") && strings.Contains(string(branchDriverCalls), "--branch feature/current"), "configured branch driver must receive ready branch delete calls")

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

func installHarnessFakeNeonBranchDriver(root string) (string, string, error) {
	bin := filepath.Join(root, "fake-neon-driver")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", "", err
	}
	callLog := filepath.Join(bin, "branch-driver.calls.log")
	driver := filepath.Join(bin, "neon-branch-driver")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %s
case "$1" in
  ensure|reset|restore)
    printf '{"status":"ready","message":"harness driver ready","endpoint":{"host":"127.0.0.1","port":55433,"database":"neon_harness","role":"cloud_admin","sslmode":"disable","source":"harness-driver"}}\n'
    exit 0
    ;;
  delete)
    printf '{"status":"deleted","message":"harness driver deleted"}\n'
    exit 0
    ;;
esac
echo "unexpected branch driver $*" >&2
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
    printf 'onlava-neon-compute\tUp 2 minutes\n'
  elif [ "$state" = "stopped" ]; then
    printf 'onlava-neon-compute\tExited (0) 1 second ago\n'
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
