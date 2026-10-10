package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

const harnessWorktreeGitProbeName = "worktree Git lifecycle probe"

type harnessWorktreeGitCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessWorktreeGitProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessWorktreeGitProbeStepWithCheck(ctx, repoRoot, runHarnessWorktreeGitProbeCheck)
}

func runHarnessWorktreeGitProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessWorktreeGitCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessWorktreeGitProbeName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix the real Git worktree lifecycle, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessWorktreeGitProbeCheck(ctx context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, resultErr error) {
	root, err := os.MkdirTemp("", "scenery-worktree-git-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		cleanupErr := os.RemoveAll(root)
		if cleanupErr == nil {
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				cleanupErr = fmt.Errorf("git probe root remains after cleanup: %s: %v", root, err)
			}
		}
		if cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("git probe cleanup: %w", cleanupErr))
		}
		if summary != nil {
			summary["owned_fixture_root"] = root
			summary["owned_cleanup_verified"] = cleanupErr == nil
		}
	}()
	restoreEnv := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(filepath.Join(root, "agent"))})
	defer restoreEnv()
	appRoot := filepath.Join(root, "demo")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return nil, nil, err
	}
	const appConfig = `{"name":"demo","envs":{"local":{"default":true}}}`
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(appConfig), 0o644); err != nil {
		return nil, nil, err
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "harness@example.invalid"},
		{"config", "user.name", "Scenery Harness"},
		{"add", ".scenery.json"},
		{"commit", "-m", "initial"},
	} {
		if _, err := runHarnessGit(ctx, appRoot, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}

	created := make([]worktreeCreateResult, 0, 2)
	for _, name := range []string{"pricing-agent", "content-agent"} {
		var output bytes.Buffer
		if err := runProduct(ctx, repoRoot, &output, "worktree", "create", name, "--from", "main", "--app-root", appRoot, "-o", "json"); err != nil {
			return nil, nil, err
		}
		var result worktreeCreateResult
		if err := decodeCLIJSON(output.Bytes(), &result); err != nil {
			return nil, nil, err
		}
		if !result.OK {
			return nil, nil, fmt.Errorf("create %s result = %+v", name, result)
		}
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.worktree.create.schema.json"), result); len(diagnostics) != 0 {
			return nil, nil, fmt.Errorf("create %s schema validation failed: %s", name, strings.Join(diagnostics, "; "))
		}
		if _, err := os.Stat(filepath.Join(result.Path, ".scenery", "worktree-db.json")); !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("create %s unexpectedly wrote database pin: %v", name, err)
		}
		created = append(created, result)
	}
	if created[0].Path == created[1].Path {
		return nil, nil, fmt.Errorf("created worktrees share path %q", created[0].Path)
	}
	var listOutput bytes.Buffer
	if err := runProduct(ctx, repoRoot, &listOutput, "worktree", "list", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, nil, err
	}
	var listed worktreeListResult
	if err := decodeCLIJSON(listOutput.Bytes(), &listed); err != nil {
		return nil, nil, err
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.worktree.list.schema.json"), listed); len(diagnostics) != 0 {
		return nil, nil, fmt.Errorf("worktree list schema validation failed: %s", strings.Join(diagnostics, "; "))
	}
	found := map[string]bool{}
	for _, record := range listed.Worktrees {
		for _, result := range created {
			recordPath, recordErr := filepath.EvalSymlinks(record.Path)
			resultPath, resultErr := filepath.EvalSymlinks(result.Path)
			if recordErr == nil && resultErr == nil && recordPath == resultPath && record.Branch == result.Branch {
				found[result.Name] = true
			}
		}
	}
	if !found["pricing-agent"] || !found["content-agent"] {
		return nil, nil, fmt.Errorf("real Git worktrees not listed: %+v", listed.Worktrees)
	}
	for _, result := range created {
		var output bytes.Buffer
		if err := runProduct(ctx, repoRoot, &output, "worktree", "remove", result.Name, "--app-root", appRoot, "-o", "json"); err != nil {
			return nil, nil, err
		}
		var removed worktreeRemoveResult
		if err := decodeCLIJSON(output.Bytes(), &removed); err != nil {
			return nil, nil, err
		}
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.worktree.remove.schema.json"), removed); len(diagnostics) != 0 {
			return nil, nil, fmt.Errorf("remove %s schema validation failed: %s", result.Name, strings.Join(diagnostics, "; "))
		}
		if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("removed worktree %s still exists: %v", result.Path, err)
		}
	}
	var dirtyOutput bytes.Buffer
	if err := runProduct(ctx, repoRoot, &dirtyOutput, "worktree", "create", "dirty-agent", "--from", "main", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, nil, err
	}
	var dirty worktreeCreateResult
	if err := decodeCLIJSON(dirtyOutput.Bytes(), &dirty); err != nil {
		return nil, nil, err
	}
	const databaseState = `{"database":"dirty-agent","sentinel":true}`
	if err := os.MkdirAll(filepath.Join(dirty.Path, ".scenery"), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(dirty.Path, ".scenery", "worktree-db.json"), []byte(databaseState), 0o644); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(dirty.Path, ".scenery.json"), []byte(`{"name":"demo","id":"dirty","envs":{"local":{"default":true}}}`), 0o644); err != nil {
		return nil, nil, err
	}
	if err := runProduct(ctx, repoRoot, &bytes.Buffer{}, "worktree", "remove", "dirty-agent", "--app-root", appRoot, "-o", "json"); err == nil {
		return nil, nil, fmt.Errorf("real Git removal unexpectedly accepted dirty worktree")
	}
	restored, err := os.ReadFile(filepath.Join(dirty.Path, ".scenery", "worktree-db.json"))
	if err != nil {
		return nil, nil, err
	}
	if string(restored) != databaseState {
		return nil, nil, fmt.Errorf("database state after failed real Git removal = %q", restored)
	}
	ambiguity, err := checkHarnessWorktreeAmbiguity(ctx, repoRoot, appRoot, filepath.Join(root, "agent"))
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"proof":                                  "real_git_worktrees_created_listed_removed_with_ambiguity_refusal_and_dirty_bytes_preserved",
		"created_worktrees":                      []string{"pricing-agent", "content-agent"},
		"database_pins":                          0,
		"failed_remove_sentinel_bytes_preserved": true,
		"ambiguity":                              ambiguity,
	}, nil, nil
}

func checkHarnessWorktreeAmbiguity(ctx context.Context, repoRoot, appRoot, home string) (map[string]any, error) {
	branchPath := filepath.Join(filepath.Dir(appRoot), "aaa")
	defaultPath := filepath.Join(filepath.Dir(appRoot), "demo-target")
	for _, args := range [][]string{
		{"worktree", "add", "-b", "target", branchPath},
		{"worktree", "add", "-b", "different-branch", defaultPath},
	} {
		if _, err := runHarnessGit(ctx, appRoot, args...); err != nil {
			return nil, err
		}
	}
	before, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	config, err := os.ReadFile(filepath.Join(appRoot, ".scenery.json"))
	if err != nil {
		return nil, err
	}
	canonicalBranchPath, err := filepath.EvalSymlinks(branchPath)
	if err != nil {
		return nil, err
	}
	const sentinel = "private retained sentinel\n"
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	sentinelPath := filepath.Join(home, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte(sentinel), 0o600); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	err = runProduct(ctx, repoRoot, &output, "worktree", "remove", "target", "--app-root", appRoot, "-o", "json")
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 3 {
		return nil, fmt.Errorf("ambiguous real Git removal did not exit 3: %v: %s", err, &output)
	}
	envelope, err := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
	if err != nil {
		return nil, err
	}
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" || !strings.Contains(envelope.Diagnostics[0].Message, "ambiguous") {
		return nil, fmt.Errorf("ambiguity refusal = %+v", envelope.Diagnostics)
	}
	for _, path := range []string{branchPath, defaultPath} {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.Contains(envelope.Diagnostics[0].Message, canonical) {
			return nil, fmt.Errorf("ambiguity refusal omitted preserved checkout %s: %v", path, err)
		}
		contents, err := os.ReadFile(filepath.Join(path, ".scenery.json"))
		if err != nil || !bytes.Equal(contents, config) {
			return nil, fmt.Errorf("ambiguity changed checkout %s: %v", path, err)
		}
	}
	after, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain")
	if err != nil || after != before {
		return nil, fmt.Errorf("ambiguity changed Git inventory: %v: %s", err, after)
	}
	if _, err := runHarnessGit(ctx, appRoot, "worktree", "remove", defaultPath); err != nil {
		return nil, err
	}
	output.Reset()
	if err := runProduct(ctx, repoRoot, &output, "worktree", "remove", "target", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, err
	}
	var removed worktreeRemoveResult
	if err := decodeCLIJSON(output.Bytes(), &removed); err != nil || !removed.OK || removed.Path != canonicalBranchPath {
		return nil, fmt.Errorf("unique real Git removal = %+v: %v", removed, err)
	}
	if _, err := os.Stat(branchPath); !os.IsNotExist(err) {
		return nil, fmt.Errorf("unique checkout still exists: %v", err)
	}
	finalInventory, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain")
	if err != nil || strings.Contains(finalInventory, "branch refs/heads/target\n") || strings.Contains(finalInventory, "branch refs/heads/different-branch\n") {
		return nil, fmt.Errorf("unique removal retained Git registration: %v: %s", err, finalInventory)
	}
	contents, err := os.ReadFile(sentinelPath)
	if err != nil || string(contents) != sentinel {
		return nil, fmt.Errorf("git removal changed retained sentinel bytes: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "worktrees")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("git-only removal allocated worktree state: %v", err)
	}
	return map[string]any{
		"candidate_paths": []string{branchPath, defaultPath}, "diagnostic": envelope.Diagnostics[0],
		"inventory_before": before, "inventory_after_refusal": after, "inventory_after_unique_remove": finalInventory,
		"checkout_bytes_preserved": true, "retained_sentinel_preserved": true,
		"unique_remove_passed": true, "worktree_state_allocated": false,
	}, nil
}
