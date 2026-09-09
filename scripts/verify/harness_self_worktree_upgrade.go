package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/machine"
)

// Native fixtures alone receive an older identity on otherwise-current data.
// The migration itself is exercised exclusively through the public CLI.
func probeRetainedSpecUpgrade(paths localagent.WorktreePaths, run func(...string) ([]byte, error)) (evidence map[string]any, resultErr error) {
	evidence = map[string]any{}
	original, historical := map[string][]byte{}, map[string][]byte{}
	payloads := map[string][32]byte{}
	names := []string{paths.Record, paths.ControlPaths().RegistryPath}
	storageRoot := filepath.Join(paths.Directory, "storage")
	if _, err := os.Stat(storageRoot); err == nil {
		if err := filepath.WalkDir(storageRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			if strings.HasSuffix(path, ".json") && !strings.HasPrefix(entry.Name(), ".") {
				names = append(names, path)
			}
			if strings.HasSuffix(path, ".data") {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				payloads[path] = sha256.Sum256(data)
			}
			return nil
		}); err != nil {
			return evidence, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return evidence, err
	}
	committing := false
	defer func() {
		// A rejected fixture setup can restore its known-current bytes so the
		// existing verified cleanup can run. Never overwrite an applied or
		// interrupted product transaction; retain its own recovery evidence.
		if resultErr != nil && !committing {
			for path, data := range original {
				resultErr = errors.Join(resultErr, os.WriteFile(path, data, 0o600))
			}
		}
	}()
	for _, path := range names {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) && path == paths.ControlPaths().RegistryPath {
			continue
		}
		if err != nil {
			return evidence, err
		}
		original[path] = data
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return evidence, err
		}
		fields["spec_revision"], _ = json.Marshal("sha256:" + strings.Repeat("b", 64))
		historical[path], err = json.Marshal(fields)
		if err != nil {
			return evidence, err
		}
		if err := os.WriteFile(path, historical[path], 0o600); err != nil {
			return evidence, err
		}
	}
	if output, err := run("inspect", "storage", "--stats"); err == nil || !bytes.Contains(output, []byte("SCN8003")) {
		return evidence, fmt.Errorf("ordinary CLI did not reject the old worktree specification")
	}
	checkHistorical := func() error {
		for path, expected := range historical {
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, expected) {
				return fmt.Errorf("read-only upgrade changed fixture metadata: %v", err)
			}
		}
		return nil
	}
	preview, err := run("worktree", "upgrade")
	if err != nil {
		return evidence, err
	}
	var result struct {
		Revision string `json:"revision"`
		Changed  int    `json:"changed_files"`
		Applied  bool   `json:"applied"`
		Pending  bool   `json:"pending"`
		Backup   string `json:"backup"`
	}
	if err := decodeCLIJSON(preview, &result); err != nil || result.Revision == "" || result.Changed != len(historical) || result.Applied || result.Pending {
		return evidence, fmt.Errorf("invalid upgrade preview: %v", err)
	}
	if err := checkHistorical(); err != nil {
		return evidence, err
	}
	if _, err := os.Stat(filepath.Join(paths.Directory, "spec-upgrades")); !errors.Is(err, os.ErrNotExist) {
		return evidence, fmt.Errorf("preview allocated an upgrade backup")
	}
	if output, err := run("worktree", "upgrade", "--yes", "--expect-revision", "sha256:"+strings.Repeat("f", 64)); err == nil || !bytes.Contains(output, []byte("SCN8003")) {
		return evidence, fmt.Errorf("stale upgrade approval was not rejected")
	}
	if err := checkHistorical(); err != nil {
		return evidence, err
	}
	committing = true
	applied, err := run("worktree", "upgrade", "--yes", "--expect-revision", result.Revision)
	if err != nil {
		return evidence, err
	}
	if err := decodeCLIJSON(applied, &result); err != nil || !result.Applied || result.Pending || result.Backup == "" {
		return evidence, fmt.Errorf("invalid upgrade apply result: %v", err)
	}
	if !strings.HasPrefix(result.Backup, filepath.Join(paths.Directory, "spec-upgrades")+string(filepath.Separator)) {
		return evidence, fmt.Errorf("upgrade backup escaped its private fixture root")
	}
	info, err := os.Stat(result.Backup)
	if err != nil || info.Mode().Perm() != 0o600 {
		return evidence, fmt.Errorf("upgrade backup is not private: %v", err)
	}
	data, err := os.ReadFile(result.Backup)
	if err != nil {
		return evidence, err
	}
	var backup struct {
		Changes []struct {
			Path   string `json:"path"`
			Before []byte `json:"before"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(data, &backup); err != nil || len(backup.Changes) != len(historical) {
		return evidence, fmt.Errorf("upgrade backup selection differs: %v", err)
	}
	for _, change := range backup.Changes {
		path := filepath.Join(paths.Directory, change.Path)
		if !bytes.Equal(change.Before, historical[path]) {
			return evidence, fmt.Errorf("upgrade backup lost exact source metadata")
		}
		actual, err := os.ReadFile(path)
		if err != nil || !machine.ArtifactPayloadEqual(original[path], actual) {
			return evidence, fmt.Errorf("upgrade changed retained authority or object metadata: %v", err)
		}
	}
	for path, expected := range payloads {
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != expected {
			return evidence, fmt.Errorf("upgrade changed an immutable object payload: %v", err)
		}
	}
	current, err := run("worktree", "upgrade")
	if err != nil {
		return evidence, err
	}
	if err := decodeCLIJSON(current, &result); err != nil || result.Changed != 0 || result.Pending {
		return evidence, fmt.Errorf("current upgrade preview is not a no-op: %v", err)
	}
	evidence["metadata_files"] = len(historical)
	evidence["unchanged_object_payloads"] = len(payloads)
	evidence["preview_read_only"] = true
	evidence["stale_approval_rejected"] = true
	evidence["exact_private_backup"] = true
	evidence["authority_payload_unchanged"] = true
	evidence["current_preview_noop"] = true
	return evidence, nil
}
