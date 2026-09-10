package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	localagent "scenery.sh/internal/agent"
)

// This probe owns a disposable repository, linked worktree and agent home.
// Assertions use the public CLI, never the obsolete shared-cell layout.
func runHarnessStorageIsolationProbe(ctx context.Context, repoRoot, binary string) (summary map[string]any, err error) {
	summary = map[string]any{"fixture": "testdata/apps/storage-basic", "cleanup": "pending"}
	base, err := os.MkdirTemp("", "scenery-storage-probe-")
	if err != nil {
		return summary, err
	}
	defer func() {
		if summary["process_cleanup"] == "failed" {
			summary["cleanup"] = "failed"
			summary["retained_probe_root"] = base
			return
		}
		cleanupErr := os.RemoveAll(base)
		err = errors.Join(err, cleanupErr)
		if cleanupErr == nil {
			summary["cleanup"] = "passed"
		} else {
			summary["cleanup"] = "failed"
		}
	}()
	rootA, rootB := filepath.Join(base, "a"), filepath.Join(base, "b")
	home := filepath.Join(base, "agent-home")
	if err := prepareStorageProbeWorktrees(ctx, repoRoot, rootA, rootB); err != nil {
		return summary, err
	}
	run := func(root string, args ...string) (string, error) {
		command := append([]string{binary}, args...)
		command = append(command, "--app-root", root, "-o", "json")
		out, stderr, err := runHarnessStorageProbeCommand(ctx, repoRoot, home, command)
		if err != nil {
			return out, fmt.Errorf("storage probe %v: %w: %s", args, err, tailString(firstNonEmpty(stderr, out), 4096))
		}
		return out, nil
	}
	// Unallocated reads must not create the agent home, locks or owner records.
	for _, root := range []string{rootA, rootB} {
		if _, err := run(root, "inspect", "storage"); err != nil {
			return summary, err
		}
		if _, err := run(root, "storage", "ls", "app", "--tenant", "probe"); err != nil {
			return summary, err
		}
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		return summary, fmt.Errorf("unallocated read created state or failed inspection: %v", err)
	}
	summary["unallocated_reads_no_state"] = "passed"
	input, output := filepath.Join(base, "input"), filepath.Join(base, "output")
	put := func(root, body string) (string, error) {
		if err := os.WriteFile(input, []byte(body), 0o600); err != nil {
			return "", err
		}
		return run(root, "storage", "put", "app", "same/key", input, "--tenant", "probe")
	}
	read := func(root, key, tenant, want string) error {
		if _, err := run(root, "storage", "get", "app", key, "--tenant", tenant, "--output", output); err != nil {
			return err
		}
		data, err := os.ReadFile(output)
		if err != nil {
			return err
		}
		if string(data) != want {
			return fmt.Errorf("storage body mismatch in %s: got %q, want %q", root, data, want)
		}
		return nil
	}
	a, err := put(rootA, "worktree A")
	if err != nil {
		return summary, err
	}
	if _, err := run(rootB, "storage", "stat", "app", "same/key", "--tenant", "probe"); err == nil {
		return summary, fmt.Errorf("unallocated worktree B read A's object")
	}
	if err := storageProbeConditionalRaces(ctx, repoRoot, binary, home, rootB, input); err != nil {
		return summary, err
	}
	summary["cross_process_conditional_writes"] = "passed"
	b, err := put(rootB, "worktree B")
	if err != nil {
		return summary, err
	}
	var left, right struct {
		Scope struct {
			WorktreeKey string `json:"worktree_key"`
			Incarnation string `json:"incarnation"`
		} `json:"scope"`
	}
	if err := decodeCLIJSON([]byte(a), &left); err != nil {
		return summary, err
	}
	if err := decodeCLIJSON([]byte(b), &right); err != nil {
		return summary, err
	}
	if left.Scope.WorktreeKey == "" || left.Scope.WorktreeKey == right.Scope.WorktreeKey || left.Scope.Incarnation == "" || left.Scope.Incarnation == right.Scope.Incarnation {
		return summary, fmt.Errorf("linked worktrees did not receive independent storage identities")
	}
	if err := read(rootA, "same/key", "probe", "worktree A"); err != nil {
		return summary, err
	}
	if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	if _, err := runHarnessGit(ctx, rootB, "switch", "-c", "storage-branch-retention"); err != nil {
		return summary, err
	}
	if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	summary["same_root_branch_change_retains_files"] = "passed"
	paths, err := localagent.PathsForWorktree(home, rootB)
	if err != nil {
		return summary, err
	}
	upgrade, err := probeRetainedSpecUpgrade(paths, func(args ...string) ([]byte, error) {
		output, err := run(rootB, args...)
		return []byte(output), err
	})
	if err != nil {
		return summary, err
	}
	if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	summary["explicit_same_schema_upgrade"] = upgrade
	if _, err := run(rootB, "storage", "get", "app", "missing-download", "--tenant", "probe", "--output", output); err == nil {
		return summary, fmt.Errorf("missing object download unexpectedly succeeded")
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "worktree B" {
		return summary, fmt.Errorf("failed CLI download changed prior output: %v", err)
	}
	summary["failed_download_preserves_existing_output"] = "passed"
	if _, err := run(rootA, "storage", "rm", "app", "same/key", "--tenant", "probe"); err != nil {
		return summary, err
	}
	if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	preview, err := run(rootA, "storage", "cleanup", "--purge")
	if err != nil {
		return summary, err
	}
	var purge struct {
		Preview struct {
			Revision string `json:"selection_revision"`
		} `json:"purge_preview"`
	}
	if err := decodeCLIJSON([]byte(preview), &purge); err != nil {
		return summary, err
	}
	if purge.Preview.Revision == "" {
		return summary, fmt.Errorf("purge preview omitted its selection revision")
	}
	if _, err := run(rootA, "storage", "cleanup", "--purge", "--yes", "--expect-revision", purge.Preview.Revision); err != nil {
		return summary, err
	}
	if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	summary["linked_worktree_write_delete_purge_isolation"] = "passed"
	// The task and CLI must use the same logical tenant/key in B.
	command := []string{binary, "task", "run", "--app-root", rootB, "service:storage-probe"}
	if out, stderr, err := runHarnessStorageProbeCommand(ctx, repoRoot, home, command); err != nil {
		return summary, fmt.Errorf("storage SDK task: %w: %s", err, tailString(firstNonEmpty(stderr, out), 4096))
	}
	if err := read(rootB, "task/probe.txt", "storage-probe", "storage task probe"); err != nil {
		return summary, err
	}
	summary["sdk_cli_tuple"] = "passed"
	archive := filepath.Join(base, "fixture.zip")
	if _, err := run(rootB, "snapshot", "save", "--storage", "--output", archive); err != nil {
		return summary, err
	}
	dryHome := filepath.Join(base, "dry-run-home")
	for _, command := range [][]string{
		{binary, "snapshot", "verify", "--input", archive, "-o", "json"},
		{binary, "snapshot", "load", "--input", archive, "--storage", "--mode", "overwrite", "--yes", "--dry-run", "--app-root", rootA, "-o", "json"},
	} {
		if out, stderr, err := runHarnessStorageProbeCommand(ctx, repoRoot, dryHome, command); err != nil {
			return summary, fmt.Errorf("read-only snapshot probe: %w: %s", err, firstNonEmpty(stderr, out))
		}
	}
	if _, err := os.Stat(dryHome); !errors.Is(err, os.ErrNotExist) {
		return summary, fmt.Errorf("snapshot verify/dry-run created target state: %v", err)
	}
	summary["snapshot_verify_dry_run_no_target_state"] = "passed"
	loaded, err := run(rootA, "snapshot", "load", "--storage", "--input", archive, "--mode", "overwrite", "--yes")
	if err != nil {
		return summary, err
	}
	var restored struct {
		Storage struct {
			Scope struct {
				Incarnation string `json:"incarnation"`
			} `json:"scope"`
			Files, Cloned, Copied int64
		} `json:"storage"`
	}
	if err := decodeCLIJSON([]byte(loaded), &restored); err != nil {
		return summary, err
	}
	if restored.Storage.Scope.Incarnation == "" || restored.Storage.Scope.Incarnation == left.Scope.Incarnation || restored.Storage.Files < 3 || restored.Storage.Cloned+restored.Storage.Copied != restored.Storage.Files {
		return summary, fmt.Errorf("snapshot restore did not report a fresh incarnation and actual materialization: %s", loaded)
	}
	if err := os.Remove(archive); err != nil {
		return summary, err
	}
	if err := read(rootA, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	if err := read(rootA, "task/probe.txt", "storage-probe", "storage task probe"); err != nil {
		return summary, err
	}
	if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
		return summary, err
	}
	summary["snapshot_restore_source_eviction"] = "passed"
	summary["snapshot_cloned"] = restored.Storage.Cloned
	summary["snapshot_copied"] = restored.Storage.Copied
	if err := storageProbeLegacyExport(ctx, repoRoot, binary, home, base, rootA, run); err != nil {
		return summary, err
	}
	summary["legacy_export_verify_import_source_preserved"] = "passed"
	probeBinary := filepath.Join(base, "storage-native-probe")
	build := commandTreeContext(ctx, "go", "build", "-o", probeBinary, "./testdata/storageprobe")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		return summary, fmt.Errorf("build native storage fixture: %w: %s", err, tailString(string(output), 4096))
	}
	probe := commandTreeContext(ctx, probeBinary, "--root", filepath.Join(base, "native"))
	probe.Dir = repoRoot
	nativeOut, err := probe.CombinedOutput()
	if err != nil {
		return summary, fmt.Errorf("native storage fixture: %w: %s", err, tailString(string(nativeOut), 4096))
	}
	var native map[string]any
	if err := json.Unmarshal(nativeOut, &native); err != nil {
		return summary, fmt.Errorf("decode native storage fixture: %w", err)
	}
	summary["native_boundaries"] = native
	batchBinary := filepath.Join(base, "storage-reclaim-batches")
	batchBuild := commandTreeContext(ctx, "go", "test", "-c", "-tags=scenery_storage_integration", "-o", batchBinary, "./internal/storagefs")
	batchBuild.Dir = repoRoot
	if output, err := batchBuild.CombinedOutput(); err != nil {
		return summary, fmt.Errorf("build storage batch integration: %w: %s", err, tailString(string(output), 4096))
	}
	batchProbe := commandTreeContext(ctx, batchBinary, "-test.run=^TestReclaimPressureResumesAcrossBatches$", "-test.v")
	batchProbe.Dir = repoRoot
	batchOutput, err := batchProbe.CombinedOutput()
	if err != nil {
		return summary, fmt.Errorf("storage batch integration: %w: %s", err, tailString(string(batchOutput), 4096))
	}
	if !bytes.Contains(batchOutput, []byte("--- PASS: TestReclaimPressureResumesAcrossBatches")) {
		return summary, fmt.Errorf("storage batch integration did not execute its required journey: %s", tailString(string(batchOutput), 4096))
	}
	summary["reclaim_pressure_260_entries_resume"] = "passed"
	restart, err := runHarnessLocalStorageRestartProbe(ctx, repoRoot, binary, rootB, filepath.Join(base, "restart-home"))
	for key, value := range restart {
		summary[key] = value
	}
	if err != nil {
		return summary, err
	}
	for _, args := range [][]string{{"down"}, {"prune", "--older-than", "1ns"}, {"prune", "--older-than", "1ns", "--state"}} {
		if _, err := run(rootB, args...); err != nil {
			return summary, err
		}
		if err := read(rootB, "same/key", "probe", "worktree B"); err != nil {
			return summary, err
		}
	}
	if _, err := runHarnessGit(ctx, rootA, "worktree", "remove", "--force", rootB); err != nil {
		return summary, err
	}
	orphan, err := run(rootB, "inspect", "storage", "--stats")
	if err != nil {
		return summary, err
	}
	var orphaned struct {
		Storage struct {
			Totals struct {
				Objects int64 `json:"objects"`
			} `json:"totals"`
		} `json:"storage"`
	}
	if err := decodeCLIJSON([]byte(orphan), &orphaned); err != nil {
		return summary, err
	}
	if orphaned.Storage.Totals.Objects < 3 {
		return summary, fmt.Errorf("git removal lost retained objects: %s", orphan)
	}
	preview, err = run(rootB, "storage", "cleanup", "--purge")
	if err != nil {
		return summary, err
	}
	if err := decodeCLIJSON([]byte(preview), &purge); err != nil {
		return summary, err
	}
	if _, err := run(rootB, "storage", "cleanup", "--purge", "--yes", "--expect-revision", purge.Preview.Revision); err != nil {
		return summary, err
	}
	if err := read(rootA, "legacy.txt", "legacy-tenant", "legacy payload"); err != nil {
		return summary, err
	}
	summary["down_state_prune_git_removal_orphan_purge"] = "passed"
	return summary, nil
}

func storageProbeConditionalRaces(ctx context.Context, repo, binary, home, root, input string) error {
	if err := os.WriteFile(input, []byte("conditional race"), 0o600); err != nil {
		return err
	}
	// Every contender is a separate production CLI process, released together.
	race := func(condition ...string) (string, error) {
		type result struct {
			out, stderr string
			err         error
		}
		start, results := make(chan struct{}), make(chan result, 4)
		for range 4 {
			go func() {
				<-start
				args := []string{binary, "storage", "put", "app", "race/key", input, "--tenant", "probe", "--app-root", root, "-o", "json"}
				args = append(args, condition...)
				out, stderr, err := runHarnessStorageProbeCommand(ctx, repo, home, args)
				results <- result{out, stderr, err}
			}()
		}
		close(start)
		wins, winner := 0, ""
		var failures error
		for range 4 {
			r := <-results
			if r.err == nil {
				wins++
				winner = r.out
				continue
			}
			var envelope struct {
				OK          bool `json:"ok"`
				Diagnostics []struct {
					Code string `json:"code"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal([]byte(r.out), &envelope); err != nil || envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" {
				failures = errors.Join(failures, fmt.Errorf("conditional contender failed unexpectedly: %v: %s", r.err, firstNonEmpty(r.stderr, r.out)))
			}
		}
		if wins != 1 {
			failures = errors.Join(failures, fmt.Errorf("conditional race had %d successes, want exactly one", wins))
		}
		return winner, failures
	}
	winner, err := race("--if-absent")
	if err != nil {
		return err
	}
	var object struct {
		Object struct {
			ETag string `json:"etag"`
		} `json:"object"`
	}
	if err := decodeCLIJSON([]byte(winner), &object); err != nil {
		return err
	}
	if object.Object.ETag == "" {
		return fmt.Errorf("winning create omitted its version")
	}
	_, err = race("--if-match", object.Object.ETag)
	if err != nil {
		return err
	}
	start, results := make(chan struct{}), make(chan error, 4)
	for i := range 4 {
		go func() {
			<-start
			args := []string{binary, "storage", "rm", "app", "race/key"}
			if i%2 == 0 {
				args = []string{binary, "storage", "put", "app", "race/key", input}
			}
			args = append(args, "--tenant", "probe", "--app-root", root, "-o", "json")
			out, stderr, err := runHarnessStorageProbeCommand(ctx, repo, home, args)
			if err != nil {
				err = fmt.Errorf("unconditional put/delete contender: %w: %s", err, firstNonEmpty(stderr, out))
			}
			results <- err
		}()
	}
	close(start)
	var failures error
	for range 4 {
		failures = errors.Join(failures, <-results)
	}
	if failures != nil {
		return failures
	}
	// Recreate after either valid final state (object or absence), then the
	// following snapshot validates its complete reference and payload digest.
	out, stderr, err := runHarnessStorageProbeCommand(ctx, repo, home, []string{binary, "storage", "put", "app", "race/key", input, "--tenant", "probe", "--app-root", root, "-o", "json"})
	if err != nil {
		return fmt.Errorf("post-race publication: %w: %s", err, firstNonEmpty(stderr, out))
	}
	return nil
}

func prepareStorageProbeWorktrees(ctx context.Context, repo, rootA, rootB string) error {
	for _, name := range []string{".scenery.json", ".gitignore", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn", "service/tasks/storage-probe.task.go"} {
		data, err := os.ReadFile(filepath.Join(repo, "testdata/apps/storage-basic", name))
		if err != nil {
			return err
		}
		if name == "go.mod" {
			data = bytes.ReplaceAll(data, []byte("=> ../../.."), []byte("=> "+filepath.ToSlash(repo)))
		}
		path := filepath.Join(rootA, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "."}, {"commit", "--quiet", "-m", "Storage isolation fixture"}, {"worktree", "add", "--quiet", "-b", "probe-b", rootB}} {
		command := commandTreeContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Scenery storage probe", "-c", "user.email=storage-probe@example.invalid"}, args...)...)
		command.Dir = rootA
		if out, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("prepare storage Git worktrees: %w: %s", err, out)
		}
	}
	return nil
}
