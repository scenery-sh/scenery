package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"

	localagent "scenery.sh/internal/agent"
)

func (p *worktreeRuntimeProbe) retainedConflicts(root, sibling string) error {
	return p.scenario("A10", "retained-volume recreation and non-mutating ownership conflicts", func(e map[string]any) error {
		if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		before, err := p.record(root)
		if err != nil {
			return err
		}
		other, err := p.record(sibling)
		if err != nil {
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
		// Fault injection edits only a stopped probe's private record. Restore
		// the exact bytes even when the candidate incorrectly accepts a fault.
		conflicts := []struct {
			name string
			edit func(*localagent.WorktreeRecord)
		}{
			{"unrelated-container-name", func(r *localagent.WorktreeRecord) { r.Postgres.Container = other.Postgres.Container }},
			{"volume-mount", func(r *localagent.WorktreeRecord) { r.Postgres.VolumeMount += "-wrong" }},
			{"daemon-identity", func(r *localagent.WorktreeRecord) { r.Postgres.DaemonID += "-wrong" }},
			{"credentials", func(r *localagent.WorktreeRecord) { r.Postgres.Password += "-wrong" }},
			{"database-system-identity", func(r *localagent.WorktreeRecord) { r.Postgres.SystemID = "999999999" }},
			{"missing-sql-authority", func(r *localagent.WorktreeRecord) { r.Postgres = nil }},
		}
		var passed []string
		for _, conflict := range conflicts {
			if err := func() error {
				defer func() { _ = os.WriteFile(paths.Record, original, 0o600) }()
				var record localagent.WorktreeRecord
				if err := json.Unmarshal(original, &record); err != nil {
					return err
				}
				conflict.edit(&record)
				encoded, err := json.Marshal(record)
				if err != nil {
					return err
				}
				if err := os.WriteFile(paths.Record, encoded, 0o600); err != nil {
					return err
				}
				if _, err := p.run(root, p.binaryForRoot(root), "db", "server", "start", "--app-root", root, "-o", "json"); err == nil {
					return fmt.Errorf("%s conflict was accepted", conflict.name)
				}
				after, err := os.ReadFile(paths.Record)
				if err != nil || !bytes.Equal(encoded, after) {
					return fmt.Errorf("%s conflict rewrote private state", conflict.name)
				}
				return nil
			}(); err != nil {
				return err
			}
			passed = append(passed, conflict.name)
		}
		e["rejected_without_state_rewrite"] = passed
		if err := p.verifiedContainerCommand(before, "stop", "--time", "10"); err != nil {
			return err
		}
		// Occupy the old publication after removing only this verified
		// container, forcing a genuinely different dynamic endpoint.
		if err := p.verifiedContainerCommand(before, "rm"); err != nil {
			return err
		}
		listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(before.Postgres.Port)))
		if err != nil {
			return err
		}
		defer func() { _ = listener.Close() }()
		runtime, err := p.up(root)
		if err != nil {
			return err
		}
		after, err := p.record(root)
		if err != nil {
			return err
		}
		x, y := before.Postgres, after.Postgres
		if x.InstanceID != y.InstanceID || x.Volume != y.Volume || x.VolumeCreatedAt != y.VolumeCreatedAt || x.SystemID != y.SystemID || x.Password != y.Password || x.ContainerID == y.ContainerID || x.Port == y.Port {
			return fmt.Errorf("recreation failed to preserve data identity or refresh container and endpoint")
		}
		if _, err := p.verify(root, runtime, "persisted"); err != nil {
			return err
		}
		e["old_container"], e["new_container"] = x.ContainerID, y.ContainerID
		e["old_port"], e["new_port"], e["volume"], e["system_id"] = x.Port, y.Port, y.Volume, y.SystemID
		return nil
	})
}
