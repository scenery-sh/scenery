package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) interruptedProvisioning(source string) error {
	return p.scenario("A11", "real process interruption at durable provisioning and restore checkpoints", func(e map[string]any) error {
		binary, control, ack, provenance, err := p.buildCheckpointVariant()
		if err != nil {
			return err
		}
		e["binary_provenance"] = provenance
		var checkpoints []map[string]any
		for _, phase := range []string{"pending", "volume", "container", "endpoint", "restore-sql-intent"} {
			root := filepath.Join(p.root, "interrupt-"+phase)
			if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-interrupt-"+phase, root); err != nil {
				return err
			}
			p.roots = append(p.roots, root)
			if err := os.WriteFile(control, []byte(phase), 0o600); err != nil {
				return err
			}
			if err := os.Remove(ack); err != nil && !os.IsNotExist(err) {
				return err
			}
			args := []string{"db", "server", "start", "--app-root", root, "-o", "json"}
			if phase == "restore-sql-intent" {
				args = []string{"snapshot", "load", "--input", filepath.Join(p.root, "faithful-library.zip"), "--db", "--mode", "overwrite", "--yes", "--app-root", root, "-o", "json"}
			}
			owner, err := p.killCheckpoint(root, binary, ack, args)
			if err != nil {
				return err
			}
			pending, err := p.record(root)
			if err != nil || pending.Postgres == nil {
				return fmt.Errorf("%s interruption lost durable resource intent: %v", phase, err)
			}
			if phase == "restore-sql-intent" {
				if pending.Postgres.Restore == nil || !pending.Postgres.Restore.SQLStarted {
					return fmt.Errorf("interrupted restore lost its SQL intent")
				}
				if _, err := p.up(root); err == nil {
					return fmt.Errorf("ordinary startup bypassed interrupted restore")
				}
				if _, err := p.run(root, p.binary, args...); err != nil {
					return err
				}
				expected, err := p.libraryRows(source)
				if err != nil {
					return err
				}
				actual, err := p.libraryRows(root)
				if err != nil || actual != expected {
					return fmt.Errorf("resumed overwrite did not faithfully restore rows: %v", err)
				}
			}
			runtime, err := p.up(root)
			if err != nil {
				return err
			}
			if err := p.get(worktreeProbeAPI(runtime) + "/books"); err != nil {
				return err
			}
			ready, err := p.record(root)
			if err != nil {
				return err
			}
			x, y := pending.Postgres, ready.Postgres
			if x.InstanceID != y.InstanceID || x.Password != y.Password || x.Volume != y.Volume || (x.VolumeCreatedAt != "" && x.VolumeCreatedAt != y.VolumeCreatedAt) || (x.ContainerID != "" && x.ContainerID != y.ContainerID) || y.Phase != "ready" || y.Restore != nil {
				return fmt.Errorf("%s retry replaced resource intent or failed to complete", phase)
			}
			checkpoints = append(checkpoints, map[string]any{"checkpoint": phase, "killed_pid": owner.PID, "instance_id": y.InstanceID, "pending_phase": x.Phase, "retained_credentials": true})
			if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
				return err
			}
		}
		e["checkpoints"] = checkpoints
		return nil
	})
}

func (p *worktreeRuntimeProbe) killCheckpoint(root, binary, ack string, args []string) (localagent.Owner, error) {
	ctx, cancel := context.WithTimeout(p.ctx, 60*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.runWithContext(ctx, root, binary, args...)
		done <- err
	}()
	for {
		encoded, err := os.ReadFile(ack)
		if err == nil {
			var owner localagent.Owner
			if json.Unmarshal(encoded, &owner) == nil {
				executable, err := filepath.EvalSymlinks(binary)
				ownerExecutable, ownerErr := filepath.EvalSymlinks(owner.Exe)
				if err != nil || ownerErr != nil || ownerExecutable != executable || localagent.VerifyOwner(owner) != nil {
					return owner, fmt.Errorf("checkpoint process fingerprint does not match the probe variant")
				}
				if _, err := p.run(root, "kill", "-KILL", fmt.Sprint(owner.PID)); err != nil {
					return owner, err
				}
				if err := <-done; err == nil {
					return owner, fmt.Errorf("interrupted checkpoint command reported success")
				}
				return owner, nil
			}
		}
		select {
		case err := <-done:
			return localagent.Owner{}, fmt.Errorf("checkpoint command exited before its gate: %w", err)
		case <-ctx.Done():
			return localagent.Owner{}, fmt.Errorf("checkpoint was not reached: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Only this release probe binary contains deterministic process gates. The
// shipped runtime has no fault flags, environment knobs, or checkpoint files.
func (p *worktreeRuntimeProbe) buildCheckpointVariant() (string, string, string, map[string]any, error) {
	dir := filepath.Join(p.root, "checkpoint-source")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", nil, err
	}
	control, ack := filepath.Join(dir, "selected-phase"), filepath.Join(dir, "reached.json")
	source := filepath.Join(p.repo, "internal/agent/worktree_record.go")
	original, err := os.ReadFile(source)
	if err != nil {
		return "", "", "", nil, err
	}
	anchor := "return atomicWriteFile(op.paths.Record, append(data, '\\n'), 0o600)"
	replacement := fmt.Sprintf(`previous, _ := op.paths.LoadRecord(record.AppID)
 err = atomicWriteFile(op.paths.Record, append(data, '\n'), 0o600)
 if err == nil && record.Postgres != nil {
  selected, _ := os.ReadFile(%q)
  p := record.Postgres
  phase := string(selected)
  reached := (phase == "pending" && p.Phase == "pending") ||
   (phase == "volume" && p.Phase == "volume") ||
   (phase == "container" && p.Phase == "container" && p.Port == 0) ||
   (phase == "endpoint" && p.Phase == "container" && p.Port != 0) ||
   (phase == "restore-sql-intent" && p.Restore != nil && p.Restore.SQLStarted) ||
   (phase == "restore-db-complete" && p.Phase == "ready" && p.Restore == nil &&
    previous.Postgres != nil && previous.Postgres.Restore != nil && previous.Postgres.Restore.SQLStarted)
  if reached {
   owner, _ := json.Marshal(CurrentOwner("release-checkpoint"))
   if err := os.WriteFile(%q, owner, 0600); err != nil { return err }
   for { time.Sleep(time.Hour) }
  }
 }
 return err`, control, ack)
	changed := bytes.Replace(original, []byte(anchor), []byte(replacement), 1)
	if bytes.Equal(original, changed) {
		return "", "", "", nil, fmt.Errorf("durable checkpoint source anchor is missing")
	}
	target := filepath.Join(dir, "worktree_record.go")
	if err := os.WriteFile(target, changed, 0o600); err != nil {
		return "", "", "", nil, err
	}
	storageSource := filepath.Join(p.repo, "internal/storagefs/restore.go")
	storageOriginal, err := os.ReadFile(storageSource)
	if err != nil {
		return "", "", "", nil, err
	}
	storageAnchor := "r.lease.owner = owner"
	storageReplacement := fmt.Sprintf(`r.lease.owner = owner
        selected, _ := os.ReadFile(%q)
        if string(selected) == "restore-storage-switched" {
            checkpointOwner, _ := json.Marshal(localagent.CurrentOwner("release-checkpoint"))
            if err := os.WriteFile(%q, checkpointOwner, 0600); err != nil { return err }
            for { time.Sleep(time.Hour) }
        }`, control, ack)
	storageChanged := bytes.Replace(storageOriginal, []byte(storageAnchor), []byte(storageReplacement), 1)
	if bytes.Equal(storageOriginal, storageChanged) {
		return "", "", "", nil, fmt.Errorf("storage generation checkpoint source anchor is missing")
	}
	storageChanged = bytes.Replace(storageChanged, []byte("\"context\""), []byte("\"context\"\n\"encoding/json\"\n\"time\"\nlocalagent \"scenery.sh/internal/agent\""), 1)
	storageTarget := filepath.Join(dir, "storage_restore.go")
	if err := os.WriteFile(storageTarget, storageChanged, 0o600); err != nil {
		return "", "", "", nil, err
	}
	overlay := filepath.Join(dir, "overlay.json")
	encoded, _ := json.Marshal(map[string]any{"Replace": map[string]string{source: target, storageSource: storageTarget}})
	if err := os.WriteFile(overlay, encoded, 0o600); err != nil {
		return "", "", "", nil, err
	}
	binary := filepath.Join(dir, "scenery-checkpoint")
	if _, err := p.run(p.repo, "go", "build", "-overlay", overlay, "-o", binary, "./cmd/scenery"); err != nil {
		return "", "", "", nil, err
	}
	digest, err := worktreeProbeFileSHA(binary)
	return binary, control, ack, map[string]any{"binary": binary, "sha256": digest, "overlay": overlay, "fault_boundary": "after fsynced worktree-record or storage-generation publication; external SIGKILL"}, err
}
