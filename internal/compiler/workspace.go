package compiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/scn"
)

type WorkspaceFile struct {
	Bytes []byte
	Mode  os.FileMode
}

type WorkspaceSnapshot map[string]WorkspaceFile

// SnapshotWorkspace reads every non-generated, non-VCS workspace file from a
// stable tree. Evolution uses the snapshot to prove plans against exact bytes.
func SnapshotWorkspace(root string) (WorkspaceSnapshot, error) {
	files := WorkspaceSnapshot{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".scenery" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace symlink is not permitted: %s", filepath.ToSlash(relative))
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("workspace entry is not a regular file: %s", filepath.ToSlash(relative))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = WorkspaceFile{Bytes: data, Mode: info.Mode()}
		return nil
	})
	return files, err
}

func EqualWorkspaceSnapshots(left, right WorkspaceSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftFile := range left {
		rightFile, ok := right[path]
		if !ok || leftFile.Mode.Perm() != rightFile.Mode.Perm() || !bytes.Equal(leftFile.Bytes, rightFile.Bytes) {
			return false
		}
	}
	return true
}

// RebaseResult binds a staged compiler result to an identical committed tree.
func RebaseResult(root string, staged *Result, checked WorkspaceSnapshot) (*Result, error) {
	if staged == nil || staged.Manifest == nil || checked == nil {
		return nil, fmt.Errorf("staged compiler result is unavailable")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	actualFiles, err := SnapshotWorkspace(absoluteRoot)
	if err != nil {
		return nil, err
	}
	if !EqualWorkspaceSnapshots(checked, actualFiles) {
		return nil, fmt.Errorf("revision_conflict: committed workspace differs from checked staging")
	}

	actual := *staged
	actual.Root = absoluteRoot
	actual.Sources = make([]*scn.Source, 0, len(staged.Sources))
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
	return &actual, nil
}
