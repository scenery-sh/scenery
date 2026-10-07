package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"strings"
	"time"
)

func (p *featureProbe) checkpointScenario() error {
	alpha := p.features["alpha"].Path
	checkpoint, err := p.git(alpha, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	prepared, err := p.prepare("alpha")
	if err != nil {
		return err
	}
	independent, err := p.prepare("gamma")
	if err != nil {
		return err
	}
	if prepared.Checkpoints[0].Commit != checkpoint {
		return errors.New("prepare did not capture the exact feature checkpoint")
	}
	for _, file := range []string{"future.txt", "staged.txt", "untracked.txt"} {
		if err := p.write(alpha, file, "later\n"); err != nil {
			return err
		}
	}
	if _, err := p.commit(alpha, "Later committed work", "future.txt"); err != nil {
		return err
	}
	if _, err := p.git(alpha, "add", "--", "staged.txt"); err != nil {
		return err
	}
	if err := p.write(alpha, "staged.txt", "unstaged over staged\n"); err != nil {
		return err
	}
	if err := p.write(alpha, "shared.txt", "later shared change\n"); err != nil {
		return err
	}
	beforeIndex, err := p.git(alpha, "diff", "--cached", "--binary")
	if err != nil {
		return err
	}
	beforeWork, err := p.git(alpha, "diff", "--binary")
	if err != nil {
		return err
	}
	first, err := p.cli("check", "--candidate", prepared.ID, "--expect-revision", prepared.Revision)
	if err != nil {
		return err
	}
	countBefore, err := os.ReadFile(filepath.Join(prepared.Path, ".scenery", "source-count"))
	if err != nil {
		return err
	}
	second, err := p.cli("check", "--candidate", prepared.ID, "--expect-revision", prepared.Revision)
	if err != nil {
		return err
	}
	countAfter, err := os.ReadFile(filepath.Join(prepared.Path, ".scenery", "source-count"))
	if err != nil {
		return err
	}
	if len(first.Candidate.Receipts) != 2 || !second.Candidate.Receipts[0].Reused || second.Candidate.Receipts[1].Reused || string(countBefore) != string(countAfter) {
		return errors.New("exact-input source reuse or fresh boundary execution failed")
	}
	if err := p.write(p.common, "probe-hold", "hold\n"); err != nil {
		return err
	}
	process, err := p.start("land", "--candidate", prepared.ID, "--yes", "--expect-revision", prepared.Revision, "-o", "json")
	if err != nil {
		return err
	}
	if err := p.wait(func() bool { return p.active() == 1 }); err != nil {
		return err
	}
	if err := p.refusal("another local main landing", "land", "--candidate", independent.ID, "--yes", "--expect-revision", independent.Revision); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(p.common, "probe-hold")); err != nil {
		return err
	}
	if err := p.await(process); err != nil {
		return err
	}
	output, err := os.ReadFile(process.output)
	if err != nil {
		return err
	}
	var landed featureProbeResult
	if err := decodeCLIJSON(output, &landed); err != nil {
		return err
	}
	if landed.Candidate.Status != "published" || landed.Candidate.Checkpoints[0].Commit != checkpoint {
		return errors.New("published receipt lost the captured checkpoint")
	}
	afterIndex, err := p.git(alpha, "diff", "--cached", "--binary")
	if err != nil {
		return err
	}
	afterWork, err := p.git(alpha, "diff", "--binary")
	if err != nil {
		return err
	}
	if beforeIndex != afterIndex || beforeWork != afterWork {
		return errors.New("landing modified the feature's staged/unstaged split")
	}
	row, err := p.row("alpha")
	if err != nil {
		return err
	}
	if row.Status != "partially_landed" || row.LastCheckpoint != checkpoint || !containsPaths(row.Outstanding, "future.txt", "staged.txt", "untracked.txt", "shared.txt") {
		return fmt.Errorf("later work disappeared from the ledger: %+v", row)
	}
	if _, err := os.Stat(filepath.Join(p.primary, "future.txt")); !os.IsNotExist(err) {
		return errors.New("later work was included in an older checkpoint")
	}
	if err := p.refusal("outstanding", "close", "alpha"); err != nil {
		return err
	}
	if err := p.refusal("advanced", "land", "--candidate", independent.ID, "--yes", "--expect-revision", independent.Revision); err != nil {
		return err
	}
	refs, err := p.git(p.primary, "ls-remote", "--heads", "origin")
	if err != nil {
		return err
	}
	if strings.Count(refs, "refs/heads/") != 1 || !strings.Contains(refs, "refs/heads/main") {
		return errors.New("landing published a feature branch")
	}
	p.passed("F2", map[string]any{"checkpoint": checkpoint, "landing": landed.Candidate.Tip, "later_committed_staged_unstaged_untracked_preserved": true, "only_main_published": true})
	p.passed("F3", map[string]any{"source_receipt_reused": true, "external_probe_reused": false, "publication_exclusive": true, "stale_main_refused": true})
	return nil
}

func (p *featureProbe) conflictScenario() error {
	conflict, err := p.prepare("beta")
	if err != nil {
		return err
	}
	if conflict.Status != "conflicts" || !containsPaths(conflict.Conflicts, "shared.txt") {
		return errors.New("overlapping feature did not expose its conflict")
	}
	ready, err := p.prepare("gamma")
	if err != nil {
		return err
	}
	if _, err := p.land(ready); err != nil {
		return err
	}
	beta, err := p.row("beta")
	if err != nil {
		return err
	}
	if beta.Status != "blocked" {
		return errors.New("unresolved conflict did not remain visible")
	}
	alpha := p.features["alpha"].Path
	if _, err := p.commit(alpha, "Complete the later feature checkpoint", "future.txt", "staged.txt", "untracked.txt", "shared.txt"); err != nil {
		return err
	}
	alphaCandidate, err := p.prepare("alpha")
	if err != nil {
		return err
	}
	if _, err := p.land(alphaCandidate); err != nil {
		return err
	}
	alphaRow, err := p.row("alpha")
	if err != nil {
		return err
	}
	if alphaRow.Landing != "fully_landed" {
		return errors.New("fully recorded checkpoint remained partial")
	}
	latest, err := p.prepare("beta")
	if err != nil {
		return err
	}
	if latest.Status != "conflicts" {
		return errors.New("fresh conflict candidate did not preserve competing work")
	}
	if err := p.write(latest.Path, "shared.txt", "resolved alpha and beta\n"); err != nil {
		return err
	}
	if _, err := p.git(latest.Path, "add", "--", "shared.txt"); err != nil {
		return err
	}
	resolved, err := p.cli("inspect", latest.ID)
	if err != nil {
		return err
	}
	oldRevision := resolved.Candidate.Revision
	if err := p.write(latest.Path, "review.txt", "reviewed correction\n"); err != nil {
		return err
	}
	if _, err := p.commit(latest.Path, "Candidate correction", "review.txt"); err != nil {
		return err
	}
	if err := p.refusal("stale", "land", "--candidate", latest.ID, "--yes", "--expect-revision", oldRevision); err != nil {
		return err
	}
	current, err := p.cli("inspect", latest.ID)
	if err != nil {
		return err
	}
	if current.Candidate.Revision == oldRevision {
		return errors.New("candidate correction retained old approval")
	}
	landed, err := p.land(*current.Candidate)
	if err != nil {
		return err
	}
	shared, err := os.ReadFile(filepath.Join(p.primary, "shared.txt"))
	if err != nil || string(shared) != "resolved alpha and beta\n" {
		return errors.New("reviewed conflict resolution did not land")
	}
	betaRow, rowErr := p.row("beta")
	if rowErr != nil || betaRow.Status != "fully_landed" {
		return fmt.Errorf("old conflict candidate obscured the landing: %+v %v", betaRow, rowErr)
	}
	if _, err := p.cli("close", "beta"); err != nil {
		return err
	}
	if _, err := os.Stat(p.features["beta"].Path); err != nil {
		return errors.New("closing a feature removed its checkout")
	}
	// Removing only this disposable, closed Git fixture must not erase its
	// published receipt or turn completed historical work into an active blocker.
	if _, err := p.git(p.primary, "worktree", "remove", "--", p.features["beta"].Path); err != nil {
		return err
	}
	closed, closedErr := p.row("beta")
	if closedErr != nil || closed.Status != "closed" || closed.LastLanding != landed.Tip {
		return fmt.Errorf("closed receipt lost after Git cleanup: %+v %v", closed, closedErr)
	}
	p.passed("F4", map[string]any{"independent_feature_landed_while_conflict_waited": true, "resolved_checkpoint": landed.Checkpoints[0].Commit, "resolution_landing": landed.Tip, "stale_approval_refused": true, "close_retained_checkout": true, "receipt_retained_after_authorized_git_cleanup": true})
	return nil
}

func (p *featureProbe) failureAndBatchScenario() error {
	delta := p.features["delta"].Path
	if err := p.write(delta, "delta.txt", "dependent feature\n"); err != nil {
		return err
	}
	if err := p.write(delta, "fail.txt", "required gate fails\n"); err != nil {
		return err
	}
	if _, err := p.commit(delta, "Failing dependent checkpoint", "delta.txt", "fail.txt"); err != nil {
		return err
	}
	failing, err := p.prepare("delta")
	if err != nil {
		return err
	}
	before, err := p.git(p.primary, "rev-parse", "main")
	if err != nil {
		return err
	}
	if err := p.refusal("did not pass", "land", "--candidate", failing.ID, "--yes", "--expect-revision", failing.Revision); err != nil {
		return err
	}
	after, err := p.git(p.primary, "rev-parse", "main")
	if err != nil || before != after {
		return errors.New("failed checks advanced main")
	}
	if err := os.Remove(filepath.Join(delta, "fail.txt")); err != nil {
		return err
	}
	if _, err := p.commit(delta, "Correct the required gate", "fail.txt"); err != nil {
		return err
	}
	if _, err := p.cli("set", "epsilon", "--stage", "ready"); err != nil {
		return err
	}
	combined, err := p.prepare("delta", "epsilon")
	if err != nil {
		return err
	}
	if len(combined.Checkpoints) != 2 || combined.Checkpoints[0].IntegratedCommit == combined.Checkpoints[1].IntegratedCommit {
		return errors.New("combined candidate lost separate feature commits")
	}
	if err := p.write(p.primary, "unrelated.txt", "unrelated primary working text\n"); err != nil {
		return err
	}
	if _, err := p.git(p.primary, "add", "--", "unrelated.txt"); err != nil {
		return err
	}
	if err := p.write(p.primary, "unrelated.txt", "unstaged primary text\n"); err != nil {
		return err
	}
	index, err := p.git(p.primary, "diff", "--cached", "--binary")
	if err != nil {
		return err
	}
	work, err := p.git(p.primary, "diff", "--binary")
	if err != nil {
		return err
	}
	landed, err := p.land(combined)
	if err != nil {
		return err
	}
	newIndex, err := p.git(p.primary, "diff", "--cached", "--binary")
	if err != nil {
		return err
	}
	newWork, err := p.git(p.primary, "diff", "--binary")
	if err != nil || newIndex != index || newWork != work {
		return errors.New("main landing changed unrelated primary work")
	}
	for _, name := range []string{"delta", "epsilon"} {
		row, err := p.row(name)
		if err != nil || row.Landing != "fully_landed" {
			return fmt.Errorf("batch checkpoint %s not fully recorded: %v %+v", name, err, row)
		}
	}
	count, err := os.ReadFile(filepath.Join(combined.Path, ".scenery", "source-count"))
	if err != nil || strings.Count(string(count), "executed") != 1 {
		return errors.New("combined candidate repeated the shared check union")
	}
	p.passed("F5", map[string]any{"failed_gate_preserved_main": true, "dependent_feature_unblocked": true, "separate_batch_commits": true, "combined_checks_executed_once": true, "primary_index_worktree_preserved": true, "batch_landing": landed.Tip})
	return nil
}

func (p *featureProbe) schedulingScenario() error {
	if err := p.write(p.common, "probe-hold", "hold\n"); err != nil {
		return err
	}
	first, err := p.start("check", "alpha", "-o", "json")
	if err != nil {
		return err
	}
	if err := p.wait(func() bool { return p.active() == 1 }); err != nil {
		return err
	}
	second, err := p.start("check", "gamma", "-o", "json")
	if err != nil {
		return err
	}
	// Confirm both CLI processes are live while only one boundary owns admission.
	select {
	case err := <-second.done:
		second.finished = true
		return fmt.Errorf("second boundary did not wait: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if p.active() != 1 {
		return errors.New("worktree-local commands exceeded shared expensive-probe admission")
	}
	if err := os.Remove(filepath.Join(p.common, "probe-hold")); err != nil {
		return err
	}
	if err := p.await(first); err != nil {
		return err
	}
	if err := p.await(second); err != nil {
		return err
	}
	if err := p.wait(func() bool { return p.active() == 0 }); err != nil {
		return err
	}
	// Reusing the same slots after the owners exit proves files alone hold no lease.
	if _, err := p.cli("check", "gamma"); err != nil {
		return err
	}
	p.passed("F6", map[string]any{"shared_probe_limit": 1, "simultaneous_feature_commands": 2, "slots_reused_after_owner_exit": true})
	return nil
}

func (p *featureProbe) watchScenario() error {
	process, err := p.start("list", "--watch", "--interval", "100ms", "-o", "jsonl")
	if err != nil {
		return err
	}
	if err := p.wait(func() bool { data, _ := os.ReadFile(process.output); return strings.Count(string(data), "\n") >= 1 }); err != nil {
		return err
	}
	if err := p.write(p.features["gamma"].Path, "watch.txt", "new outstanding work\n"); err != nil {
		return err
	}
	if err := p.wait(func() bool {
		data, _ := os.ReadFile(process.output)
		return strings.Contains(string(data), "watch.txt")
	}); err != nil {
		return err
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	if err := p.await(process); err != nil {
		return err
	}
	data, err := os.ReadFile(process.output)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i, line := range lines {
		envelope, err := machine.DecodeEvent[graph.Diagnostic]([]byte(line), currentMachineSpecRevision())
		if err != nil {
			return err
		}
		if envelope.Sequence != uint64(i+1) || envelope.Terminal != (i == len(lines)-1) {
			return errors.New("feature watch lost monotonic sequence or unique terminal summary")
		}
		if !envelope.Terminal {
			payload, _ := json.Marshal(envelope.Data)
			var output featureProbeResult
			if err := json.Unmarshal(payload, &output); err != nil || output.Overview == nil {
				return errors.New("feature watch lost its typed snapshot")
			}
		} else if envelope.Event != "summary" {
			return errors.New("feature watch did not end with its summary")
		}
	}
	if len(lines) < 3 {
		return errors.New("feature watch did not report a meaningful change and terminal summary")
	}
	telemetry, err := os.ReadFile(filepath.Join(p.root, "agent", "telemetry.jsonl"))
	if err != nil {
		return err
	}
	observed := false
	for _, line := range strings.Split(strings.TrimSpace(string(telemetry)), "\n") {
		var record struct {
			Command string `json:"command"`
			Mode    string `json:"mode"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return err
		}
		observed = observed || record.Command == "feature list" && record.Mode == "long_running"
	}
	if !observed {
		return errors.New("native feature watch telemetry lost its coarse long-running identity")
	}
	p.passed("F7", map[string]any{"watch_records": len(lines), "later_dirty_work_reported": true, "jsonl_envelopes_valid": true, "native_watch_telemetry_valid": true})
	return nil
}
