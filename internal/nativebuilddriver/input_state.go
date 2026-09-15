package nativebuilddriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const InputStateProtocol = "scenery.retained-go-input"
const InputStateRevision = 1

// InputState is the executor-independent retained Go input model. It owns only
// the last accepted capture and its immutable workspace snapshots; stock Go
// does not depend on recorded compiler actions or retained package archives.
type InputState struct {
	Protocol  string                  `json:"protocol"`
	Revision  int                     `json:"revision"`
	Workspace string                  `json:"workspace"`
	Current   Capture                 `json:"current_capture"`
	Support   map[string]RetainedFile `json:"captured_input_artifacts"`
}

func NewInputState(current Capture, stateRoot string) (*InputState, StateCommitStats, error) {
	state := &InputState{Protocol: InputStateProtocol, Revision: InputStateRevision, Workspace: current.Workspace, Support: map[string]RetainedFile{}}
	return state.Advance(current, stateRoot)
}

func (state *InputState) Validate() error {
	if state == nil || state.Protocol != InputStateProtocol || state.Revision != InputStateRevision || state.Workspace == "" {
		return fmt.Errorf("retained input state identity is incomplete")
	}
	if state.Current.Protocol != ProtocolVersion || state.Current.Digest == "" || len(state.Current.Packages) == 0 || !samePath(state.Workspace, state.Current.Workspace) {
		return fmt.Errorf("retained input capture is incomplete")
	}
	for original, snapshot := range state.Current.SnapshotFiles {
		digest := state.Current.Files[original]
		retained, ok := state.Support[snapshot]
		if original == "" || snapshot == "" || digest == "" || !ok || retained.Digest != digest {
			return fmt.Errorf("retained input snapshot identity is incomplete for %s", original)
		}
	}
	return nil
}

func (state *InputState) ValidateArtifacts() error {
	if err := state.Validate(); err != nil {
		return err
	}
	return validateRetainedFiles(state.Support)
}

func (state *InputState) RetainedCapture(ctx context.Context, goTool, snapshotRoot string, env []string, buildFlags []string) (Capture, error) {
	if err := state.Validate(); err != nil {
		return Capture{}, err
	}
	return retainedCapture(ctx, goTool, state.Workspace, state.Current, snapshotRoot, env, buildFlags)
}

func (state *InputState) CheckEligibility(current Capture) ([]string, string) {
	if state == nil {
		return nil, "retained_input_state_missing"
	}
	return inputEligibility(state.Workspace, state.Current, current)
}

// Advance materializes only newly captured workspace snapshots. Previously
// validated content-addressed snapshots retain their metadata without a byte
// reread, while the returned counters describe every new hash and reuse.
func (state *InputState) Advance(current Capture, stateRoot string) (*InputState, StateCommitStats, error) {
	var stats StateCommitStats
	if current.Protocol != ProtocolVersion || current.Workspace == "" || current.Digest == "" || len(current.Packages) == 0 {
		return nil, stats, fmt.Errorf("cannot advance incomplete retained input capture")
	}
	if state.Workspace != "" && !samePath(state.Workspace, current.Workspace) {
		return nil, stats, fmt.Errorf("retained input workspace changed")
	}
	previous := cloneRetainedFiles(state.Support)
	next := &InputState{
		Protocol: InputStateProtocol, Revision: InputStateRevision, Workspace: current.Workspace,
		Current: cloneCaptureValue(current), Support: cloneRetainedFiles(previous),
	}
	for original, snapshot := range next.Current.SnapshotFiles {
		digest := next.Current.Files[original]
		if snapshot == "" || digest == "" {
			return nil, stats, fmt.Errorf("captured workspace snapshot is incomplete: %s", original)
		}
		target := filepath.Join(stateRoot, "snapshots", digestName(original+"\x00"+digest)+filepath.Ext(original))
		if retained, ok := previous[target]; ok && retained.Digest == digest {
			next.Current.SnapshotFiles[original] = target
			continue
		}
		if !samePath(snapshot, target) {
			copy, err := copyRegularMeasured(snapshot, target, &stats.HashStats)
			if err != nil {
				return nil, stats, fmt.Errorf("retain input snapshot %s: %w", original, err)
			}
			if copy.Digest != digest {
				return nil, stats, fmt.Errorf("input snapshot identity changed: %s", original)
			}
			retained, err := retainedFileMetadata(target, copy.Digest, copy.Bytes)
			if err != nil {
				return nil, stats, err
			}
			next.Support[target] = retained
		} else if next.Support[target].Digest == "" {
			digest, size, err := fileDigestMeasured(target, &stats.HashStats)
			if err != nil {
				return nil, stats, err
			}
			retained, err := retainedFileMetadata(target, digest, size)
			if err != nil {
				return nil, stats, err
			}
			next.Support[target] = retained
		}
		next.Current.SnapshotFiles[original] = target
	}
	support := make(map[string]RetainedFile, len(next.Current.SnapshotFiles))
	for original, path := range next.Current.SnapshotFiles {
		file, ok := next.Support[path]
		if !ok || file.Digest != next.Current.Files[original] {
			return nil, stats, fmt.Errorf("retained input metadata is absent: %s", path)
		}
		support[path] = file
		if prior, ok := previous[path]; ok && prior == file {
			stats.FilesReused++
			stats.BytesReused += file.Bytes
		}
	}
	next.Support = support
	if err := next.Validate(); err != nil {
		return nil, stats, err
	}
	return next, stats, nil
}

func (state *InputState) PruneUnreferenced(stateRoot string) error {
	referenced := make(map[string]bool, len(state.Support))
	for path := range state.Support {
		referenced[filepath.Clean(path)] = true
	}
	directory := filepath.Join(filepath.Clean(stateRoot), "snapshots")
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("retained input state contains non-regular file: %s", path)
		}
		if !referenced[filepath.Clean(path)] {
			return os.Remove(path)
		}
		return nil
	})
}

func inputEligibility(workspace string, baseline, current Capture) ([]string, string) {
	if current.Reason != "" {
		return nil, current.Reason
	}
	if current.GoVersion != baseline.GoVersion || current.GoToolDigest != baseline.GoToolDigest {
		return nil, "toolchain_changed"
	}
	if !equalStrings(current.BuildFlags, baseline.BuildFlags) || !equalStringMaps(current.Environment, baseline.Environment) || !equalStringMaps(current.RequestEnv, baseline.RequestEnv) {
		return nil, "build_configuration_changed"
	}
	if len(current.Packages) != len(baseline.Packages) {
		return nil, "package_membership_changed"
	}
	changedSet := map[string]bool{}
	for name, baselinePackage := range baseline.Packages {
		actual, ok := current.Packages[name]
		if !ok || !samePackageSelection(baselinePackage, actual) {
			return nil, "package_selection_changed"
		}
	}
	for path, baselineDigest := range baseline.Files {
		actual, ok := current.Files[path]
		if !ok {
			return nil, "input_missing"
		}
		if actual == baselineDigest {
			continue
		}
		if !strings.HasSuffix(path, ".go") || current.Syntax[path] != baseline.Syntax[path] {
			return nil, "unsupported_input_changed"
		}
		if !withinWorkspace(workspace, path) {
			return nil, "unsupported_external_input_changed"
		}
		owner := packageForFile(current.Packages, path)
		if owner == "" || len(current.Packages[owner].CgoFiles) != 0 {
			return nil, "unsupported_native_or_unowned_change"
		}
		changedSet[owner] = true
	}
	for path := range current.Files {
		if _, ok := baseline.Files[path]; !ok {
			return nil, "input_added"
		}
	}
	if !equalStringMaps(current.Directories, baseline.Directories) {
		return nil, "package_selection_changed"
	}
	changed := make([]string, 0, len(changedSet))
	for pkg := range changedSet {
		changed = append(changed, pkg)
	}
	sort.Strings(changed)
	return changed, ""
}
