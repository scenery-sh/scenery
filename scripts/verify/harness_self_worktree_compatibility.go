package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) retainedCompatibility(root string) error {
	return p.scenario("A16", "compatible restart retains rows and incompatible durable state stays untouched", func(e map[string]any) error {
		if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		before, err := p.record(root)
		if err != nil {
			return err
		}
		original, err := os.ReadFile(paths.Record)
		if err != nil {
			return err
		}
		for _, fault := range []string{"engine-major", "durable-schema"} {
			if err := func() error {
				defer func() { _ = os.WriteFile(paths.Record, original, 0o600) }()
				var record localagent.WorktreeRecord
				if err := json.Unmarshal(original, &record); err != nil {
					return err
				}
				if fault == "engine-major" {
					record.Postgres.Major = 17
				} else {
					record.SchemaRevision = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
				}
				encoded, err := json.Marshal(record)
				if err != nil {
					return err
				}
				if err := os.WriteFile(paths.Record, encoded, 0o600); err != nil {
					return err
				}
				output, rejected := p.run(root, p.binary, "up", "--app-root", root, "--detach", "--wait", "ready", "-o", "json")
				if rejected == nil || !bytes.Contains(output, []byte("SCN8003")) {
					return fmt.Errorf("%s did not produce an actionable compatibility precondition", fault)
				}
				output, rejected = p.run(root, p.binary, "worktree", "upgrade", "--app-root", root, "-o", "json")
				if rejected == nil || !bytes.Contains(output, []byte("SCN8003")) {
					return fmt.Errorf("%s was accepted by the same-schema upgrade preview", fault)
				}
				after, err := os.ReadFile(paths.Record)
				if err != nil || !bytes.Equal(after, encoded) {
					return fmt.Errorf("%s rejection rewrote retained data authority", fault)
				}
				return nil
			}(); err != nil {
				return err
			}
		}
		upgrade, err := probeRetainedSpecUpgrade(paths, func(args ...string) ([]byte, error) {
			args = append(args, "--app-root", root, "-o", "json")
			return p.run(root, p.binary, args...)
		})
		if err != nil {
			return err
		}
		e["explicit_same_schema_upgrade"] = upgrade
		runtime, err := p.up(root)
		if err != nil {
			return err
		}
		if _, err := p.verify(root, runtime, "persisted"); err != nil {
			return err
		}
		after, err := p.record(root)
		if err != nil || !sameWorktreeCluster(before, after) {
			return fmt.Errorf("compatible retry changed cluster authority: %v", err)
		}
		e["rejected_without_mutation"] = []string{"engine-major", "durable-schema"}
		e["compatible_restart_preserved_rows"] = true
		return nil
	})
}
