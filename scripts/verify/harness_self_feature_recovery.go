package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// This named probe owns real Git hooks, dead CLI owners and source-identity
// checks. Ordinary tests do not create any processes or repositories.
func (p *featureProbe) recoveryScenario() error {
	gamma := p.features["gamma"].Path
	if _, err := p.commit(gamma, "Checkpoint after watch", "watch.txt"); err != nil {
		return err
	}
	candidate, err := p.prepare("gamma")
	if err != nil {
		return err
	}
	if _, err := p.cli("check", "--candidate", candidate.ID, "--expect-revision", candidate.Revision); err != nil {
		return err
	}
	original, err := os.ReadFile(filepath.Join(candidate.Path, "check.sh"))
	if err != nil {
		return err
	}
	if _, err := p.git(candidate.Path, "update-index", "--assume-unchanged", "check.sh"); err != nil {
		return err
	}
	if err := p.write(candidate.Path, "check.sh", "#!/bin/sh\nexit 0\n"); err != nil {
		return err
	}
	if err := p.refusal("bytes differ", "land", "--candidate", candidate.ID, "--yes", "--expect-revision", candidate.Revision); err != nil {
		return err
	}
	if err := p.write(candidate.Path, "check.sh", string(original)); err != nil {
		return err
	}
	if _, err := p.git(candidate.Path, "update-index", "--no-assume-unchanged", "check.sh"); err != nil {
		return err
	}
	p.passed("F8", map[string]any{"hidden_authored_byte_mutation_refused": true, "validated_candidate": candidate.ID})

	// An inherited kernel lease keeps the live child counted after its CLI dies.
	if err := p.write(p.common, "probe-hold", "hold\n"); err != nil {
		return err
	}
	first, err := p.start("land", "--candidate", candidate.ID, "--yes", "--expect-revision", candidate.Revision, "-o", "json")
	if err != nil {
		return err
	}
	if err := p.wait(func() bool { return p.active() == 1 }); err != nil {
		return err
	}
	if err := first.command.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-first.done:
		first.finished = true
	case <-time.After(3 * time.Second):
		return errors.New("killed landing owner did not exit")
	}
	second, err := p.start("land", "--candidate", candidate.ID, "--yes", "--expect-revision", candidate.Revision, "-o", "json")
	if err != nil {
		return err
	}
	select {
	case err := <-second.done:
		second.finished = true
		return fmt.Errorf("recovery should wait for the surviving probe: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if p.active() != 1 {
		return errors.New("dead CLI released admission before its live child completed")
	}
	if err := os.Remove(filepath.Join(p.common, "probe-hold")); err != nil {
		return err
	}
	if err := p.await(second); err != nil {
		return err
	}
	if err := p.wait(func() bool { return p.active() == 0 }); err != nil {
		return err
	}
	p.passed("F9", map[string]any{"dead_publication_owner_recovered": true, "surviving_child_retained_probe_admission": true, "candidate": candidate.ID})

	if err := p.write(gamma, "retry.txt", "retry exact published checkpoint\n"); err != nil {
		return err
	}
	if _, err := p.commit(gamma, "Push retry checkpoint", "retry.txt"); err != nil {
		return err
	}
	retry, err := p.prepare("gamma")
	if err != nil {
		return err
	}
	hook := filepath.Join(p.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		return err
	}
	if err := p.refusal("remains local", "land", "--candidate", retry.ID, "--yes", "--expect-revision", retry.Revision); err != nil {
		return err
	}
	local, err := p.git(p.primary, "rev-parse", "main")
	if err != nil {
		return err
	}
	remote, err := p.git(p.remote, "rev-parse", "main")
	if err != nil {
		return err
	}
	if local != retry.Tip || remote != retry.Base {
		return errors.New("failed push lost local work or changed the remote")
	}
	if err := os.Remove(hook); err != nil {
		return err
	}
	landed, err := p.land(retry)
	if err != nil {
		return err
	}
	if len(landed.Receipts) != 2 || !landed.Receipts[0].Reused || landed.Receipts[1].Reused {
		return errors.New("push retry reused the wrong evidence")
	}
	p.passed("F10", map[string]any{"failed_push_retained_validated_main": true, "exact_candidate_retry_published": true, "source_reused_external_fresh": true, "landing": landed.Tip})

	if err := p.write(gamma, "unknown.txt", "recover observed remote publication\n"); err != nil {
		return err
	}
	if _, err := p.commit(gamma, "Unknown push outcome checkpoint", "unknown.txt"); err != nil {
		return err
	}
	unknown, err := p.prepare("gamma")
	if err != nil {
		return err
	}
	hook = filepath.Join(p.remote, "hooks", "post-receive")
	if err := p.write(p.remote, "push-hold", "hold\n"); err != nil {
		return err
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho received > push-received\nwhile test -f push-hold; do sleep 0.05; done\n"), 0o700); err != nil {
		return err
	}
	process, err := p.start("land", "--candidate", unknown.ID, "--yes", "--expect-revision", unknown.Revision, "-o", "json")
	if err != nil {
		return err
	}
	if err := p.wait(func() bool { _, err := os.Stat(filepath.Join(p.remote, "push-received")); return err == nil }); err != nil {
		return err
	}
	if err := process.command.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-process.done:
		process.finished = true
	case <-time.After(3 * time.Second):
		return errors.New("unknown push CLI did not stop")
	}
	if err := os.Remove(filepath.Join(p.remote, "push-hold")); err != nil {
		return err
	}
	if _, err := p.land(unknown); err != nil {
		return err
	}
	if err := os.Remove(hook); err != nil {
		return err
	}
	row, err := p.row("gamma")
	if err != nil || row.Landing != "fully_landed" {
		return fmt.Errorf("unknown publication recovery ledger: %+v %v", row, err)
	}
	p.passed("F11", map[string]any{"observed_remote_publication_recovers_immutable_receipt": true, "landing": unknown.Tip})
	return nil
}
