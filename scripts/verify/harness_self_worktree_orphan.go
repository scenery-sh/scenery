package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) orphanCleanup(source, sibling string) error {
	return p.scenario("A12", "nested Git removal preserves exact cleanup and continuously serving controls", func(e map[string]any) (resultErr error) {
		// The parent's ignored runtime directory keeps nested fixture sources
		// out of its watcher while retaining an enclosing application marker.
		root := filepath.Join(source, ".scenery", "orphan")
		if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-orphan", root); err != nil {
			return err
		}
		p.roots = append(p.roots, root)
		if _, err := p.up(root); err != nil {
			return err
		}
		if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		before, err := p.record(root)
		if err != nil {
			return err
		}
		beforeSibling, err := p.record(sibling)
		if err != nil {
			return err
		}
		beforeParent, err := p.record(source)
		if err != nil {
			return err
		}
		parentSession, err := p.liveSession(source)
		if err != nil {
			return err
		}
		siblingSession, err := p.liveSession(sibling)
		if err != nil {
			return err
		}
		parentRuntime := detachedDevResult{PID: parentSession.OwnerPID, Session: parentSession}
		siblingRuntime := detachedDevResult{PID: siblingSession.OwnerPID, Session: siblingSession}
		stop, done := make(chan struct{}), make(chan [2][2]int, 1)
		go func() {
			var counts [2][2]int
			for {
				for i, runtime := range []detachedDevResult{parentRuntime, siblingRuntime} {
					counts[i][0]++
					if err := p.get(worktreeProbeAPI(runtime) + "/books"); err != nil {
						counts[i][1]++
					}
				}
				select {
				case <-stop:
					done <- counts
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		}()
		defer func() {
			close(stop)
			counts := <-done
			for i, name := range []string{"parent", "sibling"} {
				e[name+"_sentinel_requests"], e[name+"_sentinel_failures"] = counts[i][0], counts[i][1]
				if counts[i][0] < 2 || counts[i][1] != 0 {
					resultErr = errors.Join(resultErr, fmt.Errorf("%s sentinel completed %d requests with %d failures", name, counts[i][0], counts[i][1]))
				}
			}
		}()
		if _, err := p.run(source, "git", "worktree", "remove", "--force", root); err != nil {
			return err
		}
		output, err := p.run(source, p.binary, "ps", "--app-root", root, "-o", "json")
		if err != nil || !bytes.Contains(output, []byte(`"orphaned"`)) {
			return fmt.Errorf("removed Git worktree was not discoverable as orphaned: %v", err)
		}
		if _, err := p.run(source, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		original, err := os.ReadFile(paths.Record)
		if err != nil {
			return err
		}
		if err := func() error {
			defer func() { _ = os.WriteFile(paths.Record, original, 0o600) }()
			var invalid localagent.WorktreeRecord
			if err := json.Unmarshal(original, &invalid); err != nil {
				return err
			}
			invalid.Postgres.DaemonID += "-unavailable"
			encoded, err := json.Marshal(invalid)
			if err != nil {
				return err
			}
			if err := os.WriteFile(paths.Record, encoded, 0o600); err != nil {
				return err
			}
			if _, err := p.run(source, p.binary, "prune", "--app-root", root, "--db", "--older-than", "1ns", "-o", "json"); err == nil {
				return fmt.Errorf("prune ignored mismatched daemon authority")
			}
			after, err := os.ReadFile(paths.Record)
			if err != nil || !bytes.Equal(encoded, after) {
				return fmt.Errorf("failed prune replaced its retained retry state")
			}
			return nil
		}(); err != nil {
			return err
		}
		if _, err := p.run(source, p.binary, "prune", "--app-root", root, "--db", "--older-than", "1ns", "-o", "json"); err != nil {
			return err
		}
		after, err := p.record(root)
		if err != nil || after.Postgres != nil {
			return fmt.Errorf("successful orphan prune did not retire exact SQL authority: %v", err)
		}
		daemon, err := p.run(source, "docker", "--host", before.Postgres.DaemonEndpoint, "info", "--format", "{{.ID}}")
		if err != nil || strings.TrimSpace(string(daemon)) != before.Postgres.DaemonID {
			return fmt.Errorf("cannot verify original daemon after orphan cleanup: %v", err)
		}
		for _, object := range []struct{ kind, id string }{{"container", before.Postgres.ContainerID}, {"volume", before.Postgres.Volume}} {
			output, err := p.run(source, "docker", "--host", before.Postgres.DaemonEndpoint, object.kind, "inspect", object.id)
			if err == nil || !isMissingDockerObject(string(output), err) {
				return fmt.Errorf("selected orphan %s removal was not confirmed: %v", object.kind, err)
			}
		}
		afterSibling, err := p.record(sibling)
		if err != nil || !sameWorktreeCluster(beforeSibling, afterSibling) {
			return fmt.Errorf("orphan cleanup changed sibling authority: %v", err)
		}
		afterParent, err := p.record(source)
		if err != nil || !sameWorktreeCluster(beforeParent, afterParent) {
			return fmt.Errorf("orphan cleanup changed parent authority: %v", err)
		}
		for _, control := range []struct {
			root    string
			runtime detachedDevResult
		}{{source, parentRuntime}, {sibling, siblingRuntime}} {
			session, err := p.liveSession(control.root)
			if err != nil || session.OwnerPID != control.runtime.PID {
				return fmt.Errorf("orphan cleanup changed control owner at %s: %v", control.root, err)
			}
			if _, err := p.verify(control.root, control.runtime, "persisted"); err != nil {
				return err
			}
		}
		e["orphan_root"], e["removed_container"], e["removed_volume"] = before.AppRoot, before.Postgres.ContainerID, before.Postgres.Volume
		e["failed_cleanup_retained_state"], e["parent_unchanged"], e["sibling_unchanged"] = true, true, true
		// No process or cluster remains, and the checkout was intentionally
		// removed. Generic cleanup must not execute commands inside it.
		p.roots = p.roots[:len(p.roots)-1]
		return nil
	})
}
