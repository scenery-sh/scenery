package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	localagent "scenery.sh/internal/agent"
)

type pruneOrphanedWire struct {
	Kind           string `json:"kind"`
	SchemaRevision string `json:"schema_revision"`
	Cutoff         string `json:"cutoff"`
	Applied        bool   `json:"applied"`
	Revision       string `json:"revision"`
	Records        []struct {
		Key       string `json:"key"`
		AppRoot   string `json:"app_root"`
		Path      string `json:"path"`
		UpdatedAt string `json:"updated_at"`
		Bytes     int64  `json:"bytes"`
		Complete  bool   `json:"complete"`
		Database  *struct {
			Container string `json:"container"`
			Volume    string `json:"volume"`
		} `json:"database"`
		Selected bool   `json:"selected"`
		Removed  bool   `json:"removed"`
		Reason   string `json:"reason,omitempty"`
	} `json:"records"`
	SelectedBytes int64  `json:"selected_bytes"`
	Removed       int    `json:"removed"`
	Failed        int    `json:"failed"`
	Next          string `json:"next,omitempty"`
}

func (w pruneOrphanedWire) record(root string) *struct {
	Selected, Removed, Database bool
	Reason                      string
} {
	for _, record := range w.Records {
		if record.AppRoot == root {
			return &struct {
				Selected, Removed, Database bool
				Reason                      string
			}{record.Selected, record.Removed, record.Database != nil, record.Reason}
		}
	}
	return nil
}

func (p *worktreeRuntimeProbe) previewOrphaned(source string, apply ...string) (pruneOrphanedWire, error) {
	var result pruneOrphanedWire
	output, err := p.run(source, p.binary, append([]string{"prune", "--orphaned", "--older-than", "1ns", "-o", "json"}, apply...)...)
	if err != nil {
		return result, err
	}
	if err := decodeCLIJSON(output, &result); err != nil {
		return result, err
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(p.repo, "docs", "schemas", "scenery.prune.orphaned.schema.json"), result); len(diagnostics) != 0 {
		return result, fmt.Errorf("orphan prune schema validation failed: %v", diagnostics)
	}
	return result, nil
}

// pruneOrphaned removes the stopped cluster and retained state of the removed
// checkout at root through the one command that accepts its record after a
// retained-schema change: the record is rewritten as an older producer's
// before the preview, and only the previewed revision applies.
func (p *worktreeRuntimeProbe) pruneOrphaned(source, root string, original []byte) error {
	paths, err := localagent.PathsForWorktree(p.home, root)
	if err != nil {
		return err
	}
	var record map[string]any
	if err := json.Unmarshal(original, &record); err != nil {
		return err
	}
	record["schema_revision"] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	incompatible, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(paths.Record, incompatible, 0o600); err != nil {
		return err
	}
	if _, err := p.record(root); err == nil {
		return fmt.Errorf("the rewritten orphan record still decodes as current")
	}
	if _, err := p.run(source, p.binary, "prune", "--app-root", root, "--db", "--older-than", "1ns", "-o", "json"); err == nil {
		return fmt.Errorf("scoped database prune accepted incompatible metadata")
	}
	preview, err := p.previewOrphaned(source)
	if err != nil {
		return err
	}
	if selected := preview.record(paths.AppRoot); preview.Applied || selected == nil || !selected.Selected || !selected.Database || selected.Removed {
		return fmt.Errorf("orphan preview did not select the removed checkout with its database: %+v", selected)
	}
	if after, err := os.ReadFile(paths.Record); err != nil || !bytes.Equal(after, incompatible) {
		return fmt.Errorf("orphan preview changed retained state: %v", err)
	}
	if _, err := p.previewOrphaned(source, "--yes", "--expect-revision", "sha256:0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		return fmt.Errorf("orphan prune applied a revision its preview never reported")
	}
	applied, err := p.previewOrphaned(source, "--yes", "--expect-revision", preview.Revision)
	if err != nil {
		return err
	}
	if removed := applied.record(paths.AppRoot); !applied.Applied || applied.Failed != 0 || removed == nil || !removed.Removed {
		return fmt.Errorf("orphan prune did not remove the previewed record: %+v", removed)
	}
	return nil
}
