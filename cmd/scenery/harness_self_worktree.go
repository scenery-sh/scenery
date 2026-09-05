package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/symphony"
)

const harnessWorktreeGitProbeName = "worktree Git lifecycle probe"

type harnessWorktreeGitCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessWorktreeGitProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessWorktreeGitProbeStepWithCheck(ctx, repoRoot, runHarnessWorktreeGitProbeCheck)
}

func runHarnessWorktreeGitProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessWorktreeGitCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessWorktreeGitProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix the real Git worktree lifecycle, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessWorktreeGitProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-worktree-git-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
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
		if err := runWorktreeCommand(ctx, &output, []string{"create", name, "--from", "main", "--app-root", appRoot, "-o", "json"}); err != nil {
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
	if err := runWorktreeCommand(ctx, &listOutput, []string{"list", "--app-root", appRoot, "-o", "json"}); err != nil {
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
		if err := runWorktreeCommand(ctx, &output, []string{"remove", result.Name, "--app-root", appRoot, "-o", "json"}); err != nil {
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
	if err := runWorktreeCommand(ctx, &dirtyOutput, []string{"create", "dirty-agent", "--from", "main", "--app-root", appRoot, "-o", "json"}); err != nil {
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
	if err := runWorktreeCommand(ctx, &bytes.Buffer{}, []string{"remove", "dirty-agent", "--app-root", appRoot, "--db", "-o", "json"}); err == nil {
		return nil, nil, fmt.Errorf("real Git removal unexpectedly accepted dirty worktree")
	}
	restored, err := os.ReadFile(filepath.Join(dirty.Path, ".scenery", "worktree-db.json"))
	if err != nil {
		return nil, nil, err
	}
	if string(restored) != databaseState {
		return nil, nil, fmt.Errorf("database state after failed real Git removal = %q", restored)
	}
	symphonyCacheRoot := filepath.Join(root, "symphony-cache")
	symphonyWorkspace := filepath.Join(symphonyCacheRoot, "workspaces", "demo", "SYM-1", "repo")
	if _, err := prepareSymphonyWorkspace(ctx, symphonyCacheRoot, appRoot, symphonyWorkspace, symphonyWorkspace); err != nil {
		return nil, nil, fmt.Errorf("prepare real Symphony worktree: %w", err)
	}
	if err := os.WriteFile(filepath.Join(symphonyWorkspace, ".scenery.json"), []byte(`{"name":"demo","id":"dirty","envs":{"local":{"default":true}}}`), 0o644); err != nil {
		return nil, nil, err
	}
	symphonyUntracked := filepath.Join(symphonyWorkspace, "untracked.txt")
	if err := os.WriteFile(symphonyUntracked, []byte("remove me\n"), 0o644); err != nil {
		return nil, nil, err
	}
	reset, err := prepareSymphonyWorkspace(ctx, symphonyCacheRoot, appRoot, symphonyWorkspace, symphonyWorkspace)
	if err != nil {
		return nil, nil, fmt.Errorf("reset real Symphony worktree: %w", err)
	}
	if !reset {
		return nil, nil, fmt.Errorf("existing real Symphony worktree did not report reset")
	}
	resetConfig, err := os.ReadFile(filepath.Join(symphonyWorkspace, ".scenery.json"))
	if err != nil {
		return nil, nil, err
	}
	if string(resetConfig) != appConfig {
		return nil, nil, fmt.Errorf("reset Symphony config = %q", resetConfig)
	}
	if _, err := os.Stat(symphonyUntracked); !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("untracked Symphony file survived reset: %v", err)
	}
	canonicalSymphonyWorkspace, err := filepath.EvalSymlinks(symphonyWorkspace)
	if err != nil {
		return nil, nil, err
	}
	if err := cleanupSymphonyRunWorkspace(ctx, symphonyCacheRoot, symphony.Run{RepoRoot: appRoot, RepoWorkspace: symphonyWorkspace}); err != nil {
		return nil, nil, fmt.Errorf("cleanup real Symphony worktree: %w", err)
	}
	if _, err := os.Stat(symphonyWorkspace); !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("cleaned Symphony worktree still exists: %v", err)
	}
	worktreeList, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, nil, err
	}
	if strings.Contains(worktreeList, canonicalSymphonyWorkspace) {
		return nil, nil, fmt.Errorf("cleaned Symphony worktree registration survived: %s", worktreeList)
	}
	return map[string]any{
		"proof":                                 "real_git_worktrees_and_symphony_workspace_created_listed_removed_and_failure_rolled_back",
		"created_worktrees":                     []string{"pricing-agent", "content-agent"},
		"database_pins":                         0,
		"failed_remove_database_state_restored": true,
		"symphony_workspace_reset":              true,
		"symphony_workspace_removed":            true,
	}, nil, nil
}
