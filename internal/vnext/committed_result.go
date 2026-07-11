package vnext

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

func validateStagedWorkspace(root string, checkGenerated bool) (*Result, map[string]workspaceFile, error) {
	before, err := snapshotWorkspaceFiles(root)
	if err != nil {
		return nil, nil, err
	}
	var result *Result
	if checkGenerated {
		result, err = Check(root)
	} else {
		result, err = Compile(root)
	}
	if err != nil {
		return result, nil, err
	}
	after, err := snapshotWorkspaceFiles(root)
	if err != nil {
		return nil, nil, err
	}
	if !equalWorkspaceSnapshots(before, after) {
		return nil, nil, fmt.Errorf("revision_conflict: staged workspace changed during validation")
	}
	return result, after, nil
}

// revalidateCommittedResult proves that every non-ignored committed workspace
// file is byte-for-byte identical to the stable tree that passed full staged
// validation. This includes files outside workspace_revision projections.
func revalidateCommittedResult(root string, staged *Result, checkedFiles map[string]workspaceFile) (*Result, error) {
	if staged == nil || staged.Manifest == nil || checkedFiles == nil {
		return nil, fmt.Errorf("staged compiler result is unavailable")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	actualFiles, err := snapshotWorkspaceFiles(absoluteRoot)
	if err != nil {
		return nil, err
	}
	if !equalWorkspaceSnapshots(checkedFiles, actualFiles) {
		return nil, fmt.Errorf("revision_conflict: committed workspace differs from checked staging")
	}

	actual := *staged
	actual.Root = absoluteRoot
	actual.Sources = make([]*Source, 0, len(staged.Sources))
	for _, source := range staged.Sources {
		if source == nil || source.External {
			actual.Sources = append(actual.Sources, source)
			continue
		}
		relative, err := filepath.Rel(staged.Root, source.Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("staged source has an invalid workspace path %q", source.Path)
		}
		refreshed := *source
		refreshed.Path = filepath.Join(absoluteRoot, relative)
		actual.Sources = append(actual.Sources, &refreshed)
	}
	actual.verifiedGoFiles = nil
	actual.hasVerifiedGoFiles = false
	return &actual, nil
}

func equalWorkspaceSnapshots(left, right map[string]workspaceFile) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftFile := range left {
		rightFile, ok := right[path]
		if !ok || leftFile.mode.Perm() != rightFile.mode.Perm() || !bytes.Equal(leftFile.bytes, rightFile.bytes) {
			return false
		}
	}
	return true
}
