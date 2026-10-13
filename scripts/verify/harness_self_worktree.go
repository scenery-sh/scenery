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
	originalRoot := root
	defer func() {
		cleanupErr := os.RemoveAll(root)
		if cleanupErr == nil {
			for _, path := range []string{originalRoot, root} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("git probe root remains after cleanup: %s: %v", path, err))
				}
			}
		}
		if cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("git probe cleanup: %w", cleanupErr))
		}
		if summary != nil {
			summary["owned_fixture_root"] = root
			summary["owned_fixture_original_root"] = originalRoot
			summary["owned_cleanup_verified"] = cleanupErr == nil
		}
	}()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		root = originalRoot
		return nil, nil, err
	}
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
	if err := os.WriteFile(filepath.Join(appRoot, "revision.txt"), []byte("old revision\n"), 0o644); err != nil {
		return nil, nil, err
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "harness@example.invalid"},
		{"config", "user.name", "Scenery Harness"},
		{"add", ".scenery.json", "revision.txt"},
		{"commit", "-m", "initial"},
	} {
		if _, err := runHarnessGit(ctx, appRoot, args...); err != nil {
			return nil, nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}

	if err := os.WriteFile(filepath.Join(appRoot, "revision.txt"), []byte("current revision\n"), 0o644); err != nil {
		return nil, nil, err
	}
	if _, err := runHarnessGit(ctx, appRoot, "commit", "-am", "advance fixture revision"); err != nil {
		return nil, nil, err
	}

	created := make([]worktreeCreateResult, 0, 2)
	var ordinaryFollowups []map[string]any
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
		observation, err := checkHarnessWorktreeShellFollowup(ctx, result.NextCommand, result.Path)
		if err != nil {
			return nil, nil, err
		}
		ordinaryFollowups = append(ordinaryFollowups, observation)
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
	paths, err := checkHarnessWorktreePathFidelity(ctx, repoRoot, appRoot, filepath.Join(root, "agent"))
	if err != nil {
		return nil, nil, err
	}
	followup, err := checkHarnessWorktreeFollowup(ctx, repoRoot, appRoot, filepath.Join(root, "agent"))
	if err != nil {
		return nil, nil, err
	}
	warning, err := checkHarnessWorktreeInventoryWarning(ctx, repoRoot, root, filepath.Join(root, "agent"))
	if err != nil {
		return nil, nil, err
	}
	branches, err := checkHarnessWorktreeBranchFidelity(ctx, repoRoot, root, filepath.Join(root, "agent"))
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"branch_fidelity":                        branches,
		"inventory_warning":                      warning,
		"proof":                                  "real_git_worktrees_created_listed_removed_with_exact_paths_ambiguity_refusal_and_dirty_bytes_preserved",
		"created_worktrees":                      []string{"pricing-agent", "content-agent"},
		"database_pins":                          0,
		"failed_remove_sentinel_bytes_preserved": true,
		"ambiguity":                              ambiguity,
		"path_fidelity":                          paths,
		"create_followup":                        followup,
		"ordinary_followups":                     ordinaryFollowups,
	}, nil, nil
}

// Keep the reference porcelain separate from Git's successful warning channel.
func checkHarnessWorktreeInventoryWarning(ctx context.Context, repoRoot, fixtureRoot, home string) (map[string]any, error) {
	appRoot := filepath.Join(fixtureRoot, "warning-demo")
	child := appRoot + "-main"
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return nil, err
	}
	config := []byte(`{"name":"demo","envs":{"local":{"default":true}}}`)
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), config, 0o644); err != nil {
		return nil, err
	}
	var gitPIDs []int
	git := func(args ...string) (string, string, error) {
		cmd := commandTreeContext(ctx, "git", append([]string{"-C", appRoot}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		if cmd.Process != nil {
			gitPIDs = append(gitPIDs, cmd.Process.Pid)
		}
		return string(stdout), stderr.String(), err
	}
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "harness@example.invalid"},
		{"config", "user.name", "Scenery Harness"}, {"add", ".scenery.json"},
		{"commit", "-m", "warning fixture"}, {"worktree", "add", "-b", "other", child},
	} {
		if _, stderr, err := git(args...); err != nil {
			return nil, fmt.Errorf("warning fixture git %v: %w: %s", args, err, stderr)
		}
	}
	before, stderr, err := git("worktree", "list", "--porcelain", "-z")
	if err != nil || stderr != "" {
		return nil, fmt.Errorf("clean reference inventory: %v: %s", err, stderr)
	}
	head, _, err := git("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	var observations []map[string]any
	for _, warning := range []bool{false, true} {
		if warning {
			if _, _, err := git("config", "core.fsyncObjectFiles", "false"); err != nil {
				return nil, err
			}
		}
		stdout, stderr, err := git("worktree", "list", "--porcelain", "-z")
		if err != nil || stdout != before || (warning && !strings.Contains(stderr, "core.fsyncObjectFiles")) {
			return nil, fmt.Errorf("warning reference channels changed: %v: %s", err, stderr)
		}
		var output bytes.Buffer
		if err := runProduct(ctx, repoRoot, &output, "worktree", "list", "--app-root", appRoot, "-o", "json"); err != nil {
			return nil, err
		}
		var listed worktreeListResult
		if err := decodeCLIJSON(output.Bytes(), &listed); err != nil || !listed.OK || len(listed.Worktrees) != 2 {
			return nil, fmt.Errorf("warning public inventory = %+v: %v", listed, err)
		}
		want := map[string]string{appRoot: "main", child: "other"}
		for _, record := range listed.Worktrees {
			if branch, ok := want[record.Path]; !ok || record.Branch != branch || record.Head != strings.TrimSpace(head) {
				return nil, fmt.Errorf("warning public inventory record = %+v", record)
			}
			delete(want, record.Path)
		}
		output.Reset()
		err = runProduct(ctx, repoRoot, &output, "worktree", "remove", "main", "--app-root", appRoot, "-o", "json")
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 3 {
			return nil, fmt.Errorf("warning ambiguity did not refuse: %v: %s", err, &output)
		}
		envelope, err := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
		if err != nil || envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" || !strings.Contains(envelope.Diagnostics[0].Message, "ambiguous") {
			return nil, fmt.Errorf("warning ambiguity diagnostic = %+v: %v", envelope, err)
		}
		after, _, err := git("worktree", "list", "--porcelain", "-z")
		if err != nil || after != before {
			return nil, fmt.Errorf("warning ambiguity changed registration: %v", err)
		}
		for _, root := range []string{appRoot, child} {
			contents, err := os.ReadFile(filepath.Join(root, ".scenery.json"))
			if err != nil || !bytes.Equal(contents, config) {
				return nil, fmt.Errorf("warning ambiguity changed checkout: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(root, ".scenery", "worktree-db.json")); !os.IsNotExist(err) {
				return nil, fmt.Errorf("warning inventory allocated database pin: %v", err)
			}
		}
		observations = append(observations, map[string]any{"warning_enabled": warning, "stderr": stderr, "worktrees": listed.Worktrees, "ambiguity_refused": true, "registrations_and_bytes_preserved": true})
	}
	var output bytes.Buffer
	if err := runProduct(ctx, repoRoot, &output, "worktree", "remove", "other", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, err
	}
	var removed worktreeRemoveResult
	if err := decodeCLIJSON(output.Bytes(), &removed); err != nil || !removed.OK || removed.Path != child {
		return nil, fmt.Errorf("warning unique removal = %+v: %v", removed, err)
	}
	after, _, err := git("worktree", "list", "--porcelain", "-z")
	contents, readErr := os.ReadFile(filepath.Join(appRoot, ".scenery.json"))
	if err != nil || readErr != nil || !bytes.Equal(contents, config) || strings.Contains(after, "worktree "+child+"\x00") || !strings.Contains(after, "worktree "+appRoot+"\x00") {
		return nil, fmt.Errorf("warning unique removal scope: %v: %v", err, readErr)
	}
	if _, err := os.Lstat(child); !os.IsNotExist(err) {
		return nil, fmt.Errorf("warning unique checkout remains: %v", err)
	}
	nonGit := filepath.Join(fixtureRoot, "configured-non-git")
	if err := os.MkdirAll(nonGit, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(nonGit, ".scenery.json"), config, 0o644); err != nil {
		return nil, err
	}
	for _, jsonOutput := range []bool{true, false} {
		output.Reset()
		args := []string{"worktree", "list", "--app-root", nonGit}
		if jsonOutput {
			args = append(args, "-o", "json")
		}
		err := runProduct(ctx, repoRoot, &output, args...)
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 3 {
			return nil, fmt.Errorf("non-Git list did not refuse: %v", err)
		}
		if jsonOutput {
			envelope, decodeErr := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
			if decodeErr != nil || envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" || !strings.Contains(envelope.Diagnostics[0].Message, "not a git repository") {
				return nil, fmt.Errorf("non-Git list diagnostic = %+v: %v", envelope, decodeErr)
			}
		} else if output.Len() != 0 || !strings.Contains(err.Error(), "not a git repository") {
			return nil, fmt.Errorf("non-Git human failure lost stderr: %v", err)
		}
	}
	contents, err = os.ReadFile(filepath.Join(home, "sentinel"))
	if err != nil || string(contents) != "private retained sentinel\n" {
		return nil, fmt.Errorf("warning inventory changed retained sentinel: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "worktrees")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("warning inventory allocated authority: %v", err)
	}
	return map[string]any{"observations": observations, "unique_remove_with_warning": true, "git_failure_json_and_human_preserved": true, "reference_stdout": before, "reference_git_pids": gitPIDs, "retained_sentinel_preserved": true, "worktree_state_allocated": false}, nil
}

func checkHarnessWorktreeBranchFidelity(ctx context.Context, repoRoot, fixtureRoot, home string) (map[string]any, error) {
	var observations []map[string]any
	for i, jsonOutput := range []bool{true, false} {
		appRoot := filepath.Join(fixtureRoot, fmt.Sprintf("branch-demo-%d", i))
		prefixed := filepath.Join(fixtureRoot, fmt.Sprintf("unrelated-%d", i))
		actual := filepath.Join(fixtureRoot, fmt.Sprintf("actual-target-%d", i))
		config := []byte(`{"name":"demo","envs":{"local":{"default":true}}}`)
		if err := os.MkdirAll(appRoot, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), config, 0o644); err != nil {
			return nil, err
		}
		for _, args := range [][]string{
			{"init", "-b", "main"}, {"config", "user.email", "harness@example.invalid"},
			{"config", "user.name", "Scenery Harness"}, {"add", ".scenery.json"},
			{"commit", "-m", "branch fixture"}, {"worktree", "add", "-b", "refs/heads/target", prefixed},
		} {
			if _, err := runHarnessGit(ctx, appRoot, args...); err != nil {
				return nil, err
			}
		}
		var output bytes.Buffer
		if err := runProduct(ctx, repoRoot, &output, "worktree", "list", "--app-root", appRoot, "-o", "json"); err != nil {
			return nil, err
		}
		var listed worktreeListResult
		if err := decodeCLIJSON(output.Bytes(), &listed); err != nil || !listed.OK || len(listed.Worktrees) != 2 {
			return nil, fmt.Errorf("prefixed branch inventory = %+v: %v", listed, err)
		}
		found := false
		for _, record := range listed.Worktrees {
			if record.Path == prefixed && record.Branch == "refs/heads/target" {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("prefixed branch name not preserved")
		}
		before, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return nil, err
		}
		var missingExit int
		var missingCode string
		for _, name := range []string{"missing-control", "target"} {
			output.Reset()
			args := []string{"worktree", "remove", name, "--app-root", appRoot}
			if jsonOutput {
				args = append(args, "-o", "json")
			}
			err := runProduct(ctx, repoRoot, &output, args...)
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != 2 {
				return nil, fmt.Errorf("missing target did not refuse as invalid request: %v", err)
			}
			missingExit = exit.ExitCode()
			message := fmt.Sprintf("git worktree %q is not registered", name)
			if jsonOutput {
				envelope, decodeErr := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
				if decodeErr != nil || envelope.OK || envelope.Data != nil || len(envelope.Diagnostics) != 1 {
					return nil, fmt.Errorf("prefixed nonmatch diagnostic = %+v: %v", envelope, decodeErr)
				}
				diagnostic := envelope.Diagnostics[0]
				if diagnostic.Code != "SCN8001" || diagnostic.Message != message || diagnostic.ReportToken != "" {
					return nil, fmt.Errorf("missing target request diagnostic = %+v", diagnostic)
				}
				missingCode = diagnostic.Code
			} else if output.Len() != 0 || !strings.Contains(err.Error(), message) {
				return nil, fmt.Errorf("prefixed human nonmatch = %v", err)
			}
		}
		after, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain", "-z")
		contents, readErr := os.ReadFile(filepath.Join(prefixed, ".scenery.json"))
		if err != nil || before != after || readErr != nil || !bytes.Equal(contents, config) {
			return nil, fmt.Errorf("prefixed nonmatch changed registration/bytes: %v: %v", err, readErr)
		}
		for _, root := range []string{appRoot, prefixed} {
			if _, err := os.Lstat(filepath.Join(root, ".scenery", "worktree-db.json")); !os.IsNotExist(err) {
				return nil, fmt.Errorf("literal branch allocated database pin: %v", err)
			}
		}
		if _, err := runHarnessGit(ctx, appRoot, "worktree", "add", "-b", "target", actual); err != nil {
			return nil, err
		}
		for _, target := range []struct{ name, path string }{{"target", actual}, {filepath.Base(prefixed), prefixed}} {
			output.Reset()
			args := []string{"worktree", "remove", target.name, "--app-root", appRoot}
			if jsonOutput {
				args = append(args, "-o", "json")
			}
			if err := runProduct(ctx, repoRoot, &output, args...); err != nil {
				return nil, err
			}
			if jsonOutput {
				var removed worktreeRemoveResult
				if err := decodeCLIJSON(output.Bytes(), &removed); err != nil || !removed.OK || removed.Path != target.path {
					return nil, fmt.Errorf("literal branch removal = %+v: %v", removed, err)
				}
			} else if output.String() != "removed worktree "+target.path+"\n" {
				return nil, fmt.Errorf("literal branch human removal = %q", output.String())
			}
			inventory, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain", "-z")
			if err != nil || strings.Contains(inventory, "worktree "+target.path+"\x00") {
				return nil, fmt.Errorf("literal branch registration remains: %v", err)
			}
			if _, err := os.Lstat(target.path); !os.IsNotExist(err) {
				return nil, fmt.Errorf("literal branch checkout remains: %v", err)
			}
			for _, control := range []string{appRoot, prefixed} {
				if control == target.path {
					continue
				}
				contents, err := os.ReadFile(filepath.Join(control, ".scenery.json"))
				if err != nil || !bytes.Equal(contents, config) || !strings.Contains(inventory, "worktree "+control+"\x00") {
					return nil, fmt.Errorf("literal branch removal changed control: %v", err)
				}
			}
		}
		observations = append(observations, map[string]any{"json_output": jsonOutput, "literal_branch_listed": true, "prefixed_nonmatch_preserved": true, "ordinary_missing_exit": missingExit, "ordinary_missing_diagnostic": missingCode, "actual_target_removed": true, "prefixed_basename_remove_passed": true, "main_preserved": true})
	}
	contents, err := os.ReadFile(filepath.Join(home, "sentinel"))
	if err != nil || string(contents) != "private retained sentinel\n" {
		return nil, fmt.Errorf("literal branch changed retained sentinel: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "worktrees")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("literal branch allocated authority: %v", err)
	}
	return map[string]any{"observations": observations, "retained_sentinel_preserved": true, "worktree_state_allocated": false}, nil
}

func checkHarnessWorktreePathFidelity(ctx context.Context, repoRoot, appRoot, home string) (map[string]any, error) {
	cases := []struct{ branch, suffix string }{
		{"newline-agent", "line\nbreak"}, {"space-agent", "space path"}, {"tab-agent", "tab\tpath"},
		{"quote-agent", "quote\"path"}, {"unicode-agent", "žluťoučký"},
	}
	config, err := os.ReadFile(filepath.Join(appRoot, ".scenery.json"))
	if err != nil {
		return nil, err
	}
	wantPaths := make(map[string]string, len(cases))
	for _, tc := range cases {
		path := filepath.Join(filepath.Dir(appRoot), tc.suffix)
		if _, err := runHarnessGit(ctx, appRoot, "worktree", "add", "-b", tc.branch, path); err != nil {
			return nil, fmt.Errorf("register path %q: %w", path, err)
		}
		wantPaths[tc.branch] = path
	}
	var output bytes.Buffer
	if err := runProduct(ctx, repoRoot, &output, "worktree", "list", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, err
	}
	var listed worktreeListResult
	if err := decodeCLIJSON(output.Bytes(), &listed); err != nil || !listed.OK {
		return nil, fmt.Errorf("path list = %+v: %v", listed, err)
	}
	nativeBefore, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	if len(listed.Worktrees) != strings.Count(nativeBefore, "\x00\x00") {
		return nil, fmt.Errorf("path inventory count differs from native Git: %+v", listed.Worktrees)
	}
	seenPaths, found := map[string]bool{}, map[string]bool{}
	for _, record := range listed.Worktrees {
		if seenPaths[record.Path] {
			return nil, fmt.Errorf("duplicate listed path %q", record.Path)
		}
		seenPaths[record.Path] = true
		if want, ok := wantPaths[record.Branch]; ok {
			if found[record.Branch] || record.Path != want {
				return nil, fmt.Errorf("branch %s path = %q, want exactly %q", record.Branch, record.Path, want)
			}
			found[record.Branch] = true
		}
	}
	if len(found) != len(cases) {
		return nil, fmt.Errorf("missing path cases: got %v, want %v", found, wantPaths)
	}
	output.Reset()
	if err := runProduct(ctx, repoRoot, &output, "worktree", "remove", "newline-agent", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, err
	}
	var removed worktreeRemoveResult
	if err := decodeCLIJSON(output.Bytes(), &removed); err != nil || !removed.OK || removed.Path != wantPaths["newline-agent"] {
		return nil, fmt.Errorf("newline removal = %+v: %v", removed, err)
	}
	if _, err := os.Lstat(removed.Path); !os.IsNotExist(err) {
		return nil, fmt.Errorf("newline checkout remains: %v", err)
	}
	nativeAfter, err := runHarnessGit(ctx, appRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil || strings.Contains(nativeAfter, "worktree "+removed.Path+"\x00") || strings.Count(nativeAfter, "\x00\x00") != len(listed.Worktrees)-1 {
		return nil, fmt.Errorf("newline registration removal failed: %v: %q", err, nativeAfter)
	}
	for _, tc := range cases[1:] {
		path := wantPaths[tc.branch]
		contents, err := os.ReadFile(filepath.Join(path, ".scenery.json"))
		if err != nil || !bytes.Equal(contents, config) || !strings.Contains(nativeAfter, "worktree "+path+"\x00") {
			return nil, fmt.Errorf("control path %q or registration changed: %v", path, err)
		}
	}
	contents, err := os.ReadFile(filepath.Join(home, "sentinel"))
	if err != nil || string(contents) != "private retained sentinel\n" {
		return nil, fmt.Errorf("path removal changed retained sentinel: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "worktrees")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("path commands allocated worktree authority: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	homeEntries := make([]string, 0, len(entries))
	for _, entry := range entries {
		homeEntries = append(homeEntries, entry.Name())
	}
	return map[string]any{
		"expected_paths": wantPaths, "listed_worktrees": listed.Worktrees, "removed": removed,
		"native_inventory_before": nativeBefore, "native_inventory_after": nativeAfter,
		"exact_path_round_trips": 5, "preserved_control_paths": 4, "control_bytes_preserved": true,
		"retained_sentinel_preserved": true, "worktree_state_allocated": false, "private_home_entries": homeEntries,
	}, nil
}

func checkHarnessWorktreeAmbiguity(ctx context.Context, repoRoot, appRoot, home string) (map[string]any, error) {
	defaultPath := filepath.Join(filepath.Dir(appRoot), "demo-target")
	branchPath := defaultPath + " "
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

// The shell-local recorder proves interpretation of public output, not native up.
func checkHarnessWorktreeFollowup(ctx context.Context, repoRoot, appRoot, home string) (map[string]any, error) {
	var observations []map[string]any
	for i, directory := range []string{"demo''", "demo' space\tline\nžluťoučký"} {
		root := filepath.Join(filepath.Dir(appRoot), directory)
		if _, err := runHarnessGit(ctx, appRoot, "worktree", "add", "--detach", root, "main"); err != nil {
			return nil, err
		}
		for _, jsonOutput := range []bool{true, false} {
			name := fmt.Sprintf("followup-%d-%t", i, jsonOutput)
			target := filepath.Join(filepath.Dir(root), directory+"-"+name)
			args := []string{"worktree", "create", name, "--from", "main~1", "--app-root", root}
			if jsonOutput {
				args = append(args, "-o", "json")
			}
			var output bytes.Buffer
			if err := runProduct(ctx, repoRoot, &output, args...); err != nil {
				return nil, err
			}
			var next string
			if jsonOutput {
				var result worktreeCreateResult
				if err := decodeCLIJSON(output.Bytes(), &result); err != nil || !result.OK || result.Path != target || result.Branch != name || result.From != "main~1" {
					return nil, fmt.Errorf("literal create = %+v: %v", result, err)
				}
				if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.worktree.create.schema.json"), result); len(diagnostics) != 0 {
					return nil, fmt.Errorf("literal create schema: %s", strings.Join(diagnostics, "; "))
				}
				next = result.NextCommand
			} else {
				prefix := "created worktree " + name + " at " + target + "\nnext: "
				if !strings.HasPrefix(output.String(), prefix) || !strings.HasSuffix(output.String(), "\n") {
					return nil, fmt.Errorf("literal human create output = %q", output.String())
				}
				next = strings.TrimSuffix(strings.TrimPrefix(output.String(), prefix), "\n")
			}
			head, err := runHarnessGit(ctx, root, "rev-parse", "main~1")
			if err != nil {
				return nil, err
			}
			current, err := runHarnessGit(ctx, root, "rev-parse", "HEAD")
			if err != nil || strings.TrimSpace(current) == strings.TrimSpace(head) {
				return nil, fmt.Errorf("non-HEAD ref control is not distinct: %v", err)
			}
			content, err := os.ReadFile(filepath.Join(target, "revision.txt"))
			if err != nil || string(content) != "old revision\n" {
				return nil, fmt.Errorf("non-HEAD checkout bytes = %q: %v", content, err)
			}
			output.Reset()
			if err := runProduct(ctx, repoRoot, &output, "worktree", "list", "--app-root", root, "-o", "json"); err != nil {
				return nil, err
			}
			var listed worktreeListResult
			if err := decodeCLIJSON(output.Bytes(), &listed); err != nil || !listed.OK || listed.AppRoot != root {
				return nil, fmt.Errorf("literal create list = %+v: %v", listed, err)
			}
			found := false
			for _, record := range listed.Worktrees {
				if record.Path == target && record.Branch == name && record.Head == strings.TrimSpace(head) {
					found = true
				}
			}
			inventory, err := runHarnessGit(ctx, root, "worktree", "list", "--porcelain", "-z")
			if err != nil || !found || !strings.Contains(inventory, "worktree "+target+"\x00") {
				return nil, fmt.Errorf("literal create registration absent: %v", err)
			}
			observation, err := checkHarnessWorktreeShellFollowup(ctx, next, target)
			if err != nil {
				return nil, err
			}
			observation["json_output"] = jsonOutput
			observation["branch"] = name
			observation["head"] = strings.TrimSpace(head)
			observations = append(observations, observation)
			if _, err := os.Lstat(filepath.Join(target, ".scenery", "worktree-db.json")); !os.IsNotExist(err) {
				return nil, fmt.Errorf("follow-up create allocated database pin: %v", err)
			}
		}
		for j, ref := range []struct {
			value      string
			jsonOutput bool
		}{
			{"refs/heads/scenery-nonexistent-ref", true},
			{"--no-checkout", true},
			{"--no-checkout", false},
		} {
			before, err := runHarnessGit(ctx, root, "worktree", "list", "--porcelain", "-z")
			if err != nil {
				return nil, err
			}
			name := fmt.Sprintf("invalid-ref-%d-%d", i, j)
			var output bytes.Buffer
			args := []string{"worktree", "create", name, "--from=" + ref.value, "--app-root", root}
			if ref.jsonOutput {
				args = append(args, "-o", "json")
			}
			err = runProduct(ctx, repoRoot, &output, args...)
			if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 3 {
				return nil, fmt.Errorf("invalid ref did not refuse creation: %v", err)
			}
			if ref.jsonOutput {
				envelope, decodeErr := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
				if decodeErr != nil || envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" || strings.Contains(output.String(), "next_command") {
					return nil, fmt.Errorf("invalid ref result = %+v: %v", envelope, decodeErr)
				}
			} else if output.Len() != 0 {
				return nil, fmt.Errorf("invalid ref human refusal = %q: %v", output.String(), err)
			}
			after, err := runHarnessGit(ctx, root, "worktree", "list", "--porcelain", "-z")
			if err != nil || before != after {
				return nil, fmt.Errorf("invalid ref changed registered worktrees: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(root), directory+"-"+name)); !os.IsNotExist(err) {
				return nil, fmt.Errorf("invalid ref allocated a checkout: %v", err)
			}
			_, refErr := runHarnessGit(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
			if exit, ok := errors.AsType[*exec.ExitError](refErr); !ok || exit.ExitCode() != 1 {
				return nil, fmt.Errorf("invalid-ref branch absence not established for %s: %v", name, refErr)
			}
		}
	}
	contents, err := os.ReadFile(filepath.Join(home, "sentinel"))
	if err != nil || string(contents) != "private retained sentinel\n" {
		return nil, fmt.Errorf("follow-up changed retained sentinel: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "worktrees")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("follow-up commands allocated worktree authority: %v", err)
	}
	return map[string]any{
		"shell_output_interpretation": observations, "public_json_and_human_creates": 4,
		"invalid_ref_refusals": 6, "option_shaped_ref_refusals": 4,
		"non_head_checkout_bytes_verified": true, "registered_paths_and_heads_verified": true,
		"retained_sentinel_preserved": true, "worktree_state_allocated": false,
		"native_up_executed": false,
	}, nil
}

func checkHarnessWorktreeShellFollowup(ctx context.Context, next, target string) (map[string]any, error) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		return nil, err
	}
	const recorder = `scenery() { printf '%s\000' "$PWD" "$#" "$@"; }; `
	cmd := commandTreeContext(ctx, shell, "-c", recorder+next)
	cmd.Dir = filepath.Dir(target)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("interpret public follow-up: %w", err)
	}
	want := []byte(target + "\x001\x00up\x00")
	if !bytes.Equal(output, want) {
		return nil, fmt.Errorf("public follow-up cwd/argc/argv = %q, want %q", output, want)
	}
	return map[string]any{
		"next_command": next, "cwd": target, "argc": 1, "argv": []string{"up"},
		"shell_pid": cmd.Process.Pid, "shell_process_joined": cmd.ProcessState.Exited(),
	}, nil
}
