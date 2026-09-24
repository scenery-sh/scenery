package build

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kinds and reasons a prune reports for app-local framework state.
const (
	FrameworkPruneSource   = "source"
	FrameworkPruneProducer = "producer"
	FrameworkPruneStaging  = "staging"

	FrameworkPruneUnselected  = "unselected"
	FrameworkPruneSelected    = "selected"
	FrameworkPruneRuntime     = "runtime"
	FrameworkPruneInterrupted = "interrupted"
	FrameworkPruneLocked      = "locked"
)

// FrameworkPruneEntry reports one framework snapshot, producer directory or
// staging leftover a prune considered.
type FrameworkPruneEntry struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Bytes   int64  `json:"bytes"`
	Removed bool   `json:"removed"`
	Reason  string `json:"reason"`
}

// PruneFrameworkState removes the framework source snapshots and producers
// under the app root's `.scenery/framework` that neither its current
// selection (`.scenery/build/framework.json`) nor its retained runtime
// locator (`.scenery/build/runtime-framework.json`) names, plus staging
// directories an interrupted preparation left behind. A selection that exists
// but cannot be read fails closed: nothing is removed. A framework state root
// another process is preparing is kept and reported as locked.
func PruneFrameworkState(appRoot string) ([]FrameworkPruneEntry, error) {
	canonical, err := filepath.EvalSymlinks(appRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []FrameworkPruneEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, err
	}
	stateRoot := filepath.Join(canonical, ".scenery", "framework")
	if info, err := os.Lstat(stateRoot); errors.Is(err, os.ErrNotExist) {
		return []FrameworkPruneEntry{}, nil
	} else if err != nil {
		return nil, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("framework state root is not a non-symlink directory: %s", stateRoot)
	}
	keepSources := map[string]string{}
	keepExecutables := map[string]string{}
	for _, retained := range []struct {
		read   func(string) (FrameworkSelection, error)
		reason string
	}{{ReadFrameworkSelection, FrameworkPruneSelected}, {ReadRuntimeFramework, FrameworkPruneRuntime}} {
		selection, err := retained.read(canonical)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("refusing to prune framework state of %s: %w", canonical, err)
		}
		source := strings.TrimPrefix(selection.Source.Digest, "sha256:")
		if _, kept := keepSources[source]; !kept {
			keepSources[source] = retained.reason
		}
		executable := filepath.Dir(selection.Executable)
		if _, kept := keepExecutables[executable]; !kept {
			keepExecutables[executable] = retained.reason
		}
	}
	unlock, held, err := tryLockWorkspace(stateRoot)
	if err != nil {
		return nil, err
	}
	if held {
		return []FrameworkPruneEntry{{Path: stateRoot, Kind: FrameworkPruneSource, Reason: FrameworkPruneLocked}}, nil
	}
	defer unlock()
	report := []FrameworkPruneEntry{}
	remove := func(path, kind, reason string) error {
		item := FrameworkPruneEntry{Path: path, Kind: kind, Bytes: directoryBytes(path), Removed: true, Reason: reason}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		report = append(report, item)
		return nil
	}
	keep := func(path, kind, reason string) {
		report = append(report, FrameworkPruneEntry{Path: path, Kind: kind, Bytes: directoryBytes(path), Reason: reason})
	}
	sourceEntries, err := readFrameworkStateDir(filepath.Join(stateRoot, "source"))
	if err != nil {
		return nil, err
	}
	for _, entry := range sourceEntries {
		path := filepath.Join(stateRoot, "source", entry.Name())
		switch {
		case strings.HasPrefix(entry.Name(), ".source-"):
			if err := remove(path, FrameworkPruneStaging, FrameworkPruneInterrupted); err != nil {
				return report, err
			}
		case !isFrameworkDigestName(entry.Name()):
		case keepSources[entry.Name()] != "":
			keep(path, FrameworkPruneSource, keepSources[entry.Name()])
		default:
			if err := remove(path, FrameworkPruneSource, FrameworkPruneUnselected); err != nil {
				return report, err
			}
		}
	}
	binaryEntries, err := readFrameworkStateDir(filepath.Join(stateRoot, "bin"))
	if err != nil {
		return nil, err
	}
	for _, entry := range binaryEntries {
		path := filepath.Join(stateRoot, "bin", entry.Name())
		if !isFrameworkDigestName(entry.Name()) {
			continue
		}
		if keepSources[entry.Name()] == "" {
			if err := remove(path, FrameworkPruneProducer, FrameworkPruneUnselected); err != nil {
				return report, err
			}
			continue
		}
		platforms, err := readFrameworkStateDir(path)
		if err != nil {
			return nil, err
		}
		for _, platform := range platforms {
			executables, err := readFrameworkStateDir(filepath.Join(path, platform.Name()))
			if err != nil {
				return nil, err
			}
			for _, executable := range executables {
				producer := filepath.Join(path, platform.Name(), executable.Name())
				switch {
				case strings.HasPrefix(executable.Name(), ".build-"):
					if err := remove(producer, FrameworkPruneStaging, FrameworkPruneInterrupted); err != nil {
						return report, err
					}
				case !isFrameworkDigestName(executable.Name()):
				case keepExecutables[producer] != "":
					keep(producer, FrameworkPruneProducer, keepExecutables[producer])
				default:
					if err := remove(producer, FrameworkPruneProducer, FrameworkPruneUnselected); err != nil {
						return report, err
					}
				}
			}
		}
	}
	sort.Slice(report, func(i, j int) bool { return report[i].Path < report[j].Path })
	return report, nil
}

// readFrameworkStateDir lists the non-symlink directories of a framework state
// directory; a missing directory is empty.
func readFrameworkStateDir(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	directories := entries[:0]
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			directories = append(directories, entry)
		}
	}
	return directories, nil
}

// isFrameworkDigestName reports a directory named by a bare sha256 digest, as
// PrepareFramework names snapshots and producers.
func isFrameworkDigestName(name string) bool {
	if len(name) != 64 || strings.ToLower(name) != name {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}
