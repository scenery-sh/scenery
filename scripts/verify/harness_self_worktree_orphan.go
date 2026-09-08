package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) orphanCleanup(source, sibling string) error {
	return p.scenario("A12", "ordinary Git removal leaves discoverable data and retryable exact cleanup", func(e map[string]any) error {
		root := filepath.Join(p.root, "orphan")
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
		if _, err := p.run(source, "git", "worktree", "remove", "--force", root); err != nil {
			return err
		}
		output, err := p.run(source, p.binary, "ps", "--app-root", root, "-o", "json")
		if err != nil || !bytes.Contains(output, []byte(`"orphaned"`)) {
			return fmt.Errorf("removed Git worktree was not discoverable as orphaned: %v", err)
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
		e["orphan_root"], e["removed_container"], e["removed_volume"] = before.AppRoot, before.Postgres.ContainerID, before.Postgres.Volume
		e["failed_cleanup_retained_state"], e["sibling_unchanged"] = true, true
		// No process or cluster remains, and the checkout was intentionally
		// removed. Generic cleanup must not execute commands inside it.
		p.roots = p.roots[:len(p.roots)-1]
		return nil
	})
}
