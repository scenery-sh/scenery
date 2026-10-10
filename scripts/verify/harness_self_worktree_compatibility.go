package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
		scope, err := p.retainedUpgradeScope(paths)
		if err != nil {
			return err
		}
		e["upgrade_root_scope"] = scope
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

// Preview a nested retained fixture through the public caller before the
// existing parent migration. Only this synthetic child's exact paths are new.
func (p *worktreeRuntimeProbe) retainedUpgradeScope(parent localagent.WorktreePaths) (evidence map[string]any, resultErr error) {
	evidence = map[string]any{}
	childRoot := filepath.Join(parent.AppRoot, "upgrade-scope-child")
	child, err := localagent.PathsForWorktree(p.home, childRoot)
	if err != nil {
		return evidence, err
	}
	roots := []localagent.WorktreePaths{child}
	for _, suffix := range []string{" ", "\n"} {
		paths, err := localagent.PathsForWorktree(p.home, childRoot+suffix)
		if err != nil {
			return evidence, err
		}
		roots = append(roots, paths)
	}
	owned := []string{}
	for _, paths := range roots {
		owned = append(owned, paths.AppRoot, paths.Directory, paths.SocketDir)
	}
	for _, path := range owned {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return evidence, fmt.Errorf("upgrade scope fixture path is occupied: %s: %v", path, err)
		}
	}
	defer func() {
		for _, paths := range roots {
			for _, path := range []string{paths.AppRoot, paths.Directory} {
				resultErr = errors.Join(resultErr, os.RemoveAll(path))
			}
			if paths.SocketDir != paths.Directory {
				if err := os.Remove(paths.SocketDir); err != nil && !errors.Is(err, os.ErrNotExist) {
					resultErr = errors.Join(resultErr, err)
				}
			}
		}
		for _, path := range owned {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("upgrade scope fixture remains: %s: %v", path, err))
			}
		}
		evidence["cleanup_paths"] = owned
		evidence["cleanup_verified"] = resultErr == nil
	}()
	const childID = "upgrade-scope-child"
	configPath := filepath.Join(childRoot, ".scenery.json")
	for i, paths := range roots {
		if err := os.Mkdir(paths.AppRoot, 0o700); err != nil {
			return evidence, err
		}
		if err := paths.Prepare(); err != nil {
			return evidence, err
		}
		for _, path := range append([]string{paths.AppRoot, paths.Directory, paths.SocketDir}, filepath.Join(p.home, "worktrees", paths.Key)) {
			canonical, err := filepath.EvalSymlinks(path)
			if err != nil {
				return evidence, err
			}
			owned = append(owned, path, canonical)
		}
		for _, acquire := range []func() (*localagent.ProcessLock, error){paths.AcquireLiveLock, paths.AcquireOperationLock} {
			lock, err := acquire()
			if err != nil {
				return evidence, err
			}
			if err := lock.Release(); err != nil {
				return evidence, err
			}
		}
		appID := childID
		if i > 0 {
			appID = fmt.Sprintf("%s-%d", childID, i)
		}
		config := []byte(`{"name":"` + appID + `","envs":{"local":{"default":true}}}`)
		if err := os.WriteFile(filepath.Join(paths.AppRoot, ".scenery.json"), config, 0o600); err != nil {
			return evidence, err
		}
		record := localagent.NewWorktreeRecord(paths, appID)
		record.SpecRevision = "sha256:" + strings.Repeat("b", 64)
		data, err := json.Marshal(record)
		if err != nil {
			return evidence, err
		}
		if err := os.WriteFile(paths.Record, data, 0o600); err != nil {
			return evidence, err
		}
	}
	// Compare every private file and directory entry after each invocation,
	// including preexisting parent metadata, locks, backup and guard inventory.
	snapshot := func() (map[string]string, error) {
		files := map[string]string{}
		privateRoots := []string{parent.Directory}
		for _, paths := range roots {
			privateRoots = append(privateRoots, paths.Directory)
		}
		for _, root := range privateRoots {
			if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				files[path] = "directory"
				if !entry.IsDir() {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					files[path] = fmt.Sprintf("%x", sha256.Sum256(data))
				}
				return nil
			}); err != nil {
				return nil, err
			}
		}
		return files, nil
	}
	before, err := snapshot()
	if err != nil {
		return evidence, err
	}
	observations := []map[string]any{}
	preview := func(label, selected string, expected localagent.WorktreePaths, code string, exit int) (string, error) {
		output, runErr := p.run(parent.AppRoot, p.binary, "worktree", "upgrade", "--app-root", selected, "-o", "json")
		after, err := snapshot()
		if err != nil || !maps.Equal(before, after) {
			return "", fmt.Errorf("%s changed retained bytes or inventory: %v", label, err)
		}
		if code != "" {
			failure, ok := errors.AsType[*exec.ExitError](runErr)
			if !ok || failure.ExitCode() != exit || !bytes.Contains(output, []byte(code)) || (code == "SCN8001" && !bytes.Contains(output, []byte(expected.AppRoot))) {
				return "", fmt.Errorf("%s did not refuse at selected root: %v", label, runErr)
			}
			observations = append(observations, map[string]any{"case": label, "app_root": expected.AppRoot, "diagnostic": code, "exit": exit, "metadata_unchanged": true})
			return "", nil
		}
		if runErr != nil {
			return "", runErr
		}
		var result struct {
			AppRoot  string `json:"app_root"`
			Key      string `json:"worktree_key"`
			Revision string `json:"revision"`
			Applied  bool   `json:"applied"`
			Pending  bool   `json:"pending"`
			Backup   string `json:"backup"`
		}
		if err := decodeCLIJSON(output, &result); err != nil || result.AppRoot != expected.AppRoot || result.Key != expected.Key || result.Revision == "" || result.Applied || result.Pending || result.Backup != "" {
			return "", fmt.Errorf("%s returned a different upgrade selection: %v", label, err)
		}
		observations = append(observations, map[string]any{"case": label, "app_root": result.AppRoot, "worktree_key": result.Key, "revision": result.Revision, "metadata_unchanged": true})
		return result.Revision, nil
	}
	parentRevision, err := preview("parent before", parent.AppRoot, parent, "", 0)
	if err != nil {
		return evidence, err
	}
	if _, err := preview("configured name-only child", childRoot, child, "", 0); err != nil {
		return evidence, err
	}
	for i, paths := range roots[1:] {
		label := fmt.Sprintf("literal whitespace child %d", i)
		revision, err := preview(label, paths.AppRoot, paths, "", 0)
		if err != nil {
			return evidence, err
		}
		aliasRevision, err := preview(label+" trailing separator", paths.AppRoot+string(filepath.Separator), paths, "", 0)
		if err != nil || aliasRevision != revision {
			return evidence, fmt.Errorf("whitespace path spellings changed selection: %v", err)
		}
	}
	// Ordinary existing subdirectories still discover their enclosing app.
	ordinary := filepath.Join(parent.AppRoot, "upgrade-scope-ordinary")
	if err := os.Mkdir(ordinary, 0o700); err != nil {
		return evidence, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.Remove(ordinary)) }()
	if revision, err := preview("ordinary enclosing discovery", ordinary, parent, "", 0); err != nil || revision != parentRevision {
		return evidence, fmt.Errorf("ordinary discovery changed parent selection: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"name":"`+childID+`","id":"different-owner","envs":{"local":{"default":true}}}`), 0o600); err != nil {
		return evidence, err
	}
	if _, err := preview("configured AppID mismatch", childRoot, child, "SCN8003", 3); err != nil {
		return evidence, err
	}
	if err := os.Remove(configPath); err != nil {
		return evidence, err
	}
	if _, err := preview("missing child marker", childRoot, child, "SCN8001", 2); err != nil {
		return evidence, err
	}
	if err := os.Remove(childRoot); err != nil {
		return evidence, err
	}
	if _, err := preview("missing child checkout", childRoot, child, "SCN8001", 2); err != nil {
		return evidence, err
	}
	if revision, err := preview("parent after", parent.AppRoot, parent, "", 0); err != nil || revision != parentRevision {
		return evidence, fmt.Errorf("parent preview changed after child refusal: %v", err)
	}
	evidence["observations"] = observations
	evidence["private_inventory_unchanged"] = true
	return evidence, nil
}
