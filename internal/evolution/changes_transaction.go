package evolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

func cloneWorkspace(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp("", "scenery-change-")
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(absolute, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(absolute, path)
		if rel == "." {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".scenery" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		target := filepath.Join(temp, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace symlink is not permitted: %s", rel)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if err := os.Link(path, target); err == nil {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		destination, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(destination, source)
		closeErr := destination.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		os.RemoveAll(temp)
		return "", err
	}
	return temp, nil
}

type workspaceFile struct {
	bytes []byte
	mode  os.FileMode
}

func changedWorkspaceFiles(root, temp string) ([]SourceEdit, error) {
	before, err := snapshotWorkspaceFiles(root)
	if err != nil {
		return nil, err
	}
	after, err := snapshotWorkspaceFiles(temp)
	if err != nil {
		return nil, err
	}
	var edits []SourceEdit
	for _, relative := range stringUnion(before, after) {
		old, oldExists := before[relative]
		current, currentExists := after[relative]
		if oldExists == currentExists && bytes.Equal(old.bytes, current.bytes) && (!oldExists || old.mode.Perm() == current.mode.Perm()) {
			continue
		}
		mode := current.mode.Perm()
		if !currentExists {
			mode = old.mode.Perm()
		}
		edits = append(edits, SourceEdit{
			Path: relative, BeforeDigest: byteDigest(old.bytes), After: append([]byte(nil), current.bytes...),
			BeforeExists: oldExists, AfterExists: currentExists, Mode: uint32(mode),
		})
	}
	return edits, nil
}

func snapshotWorkspaceFiles(root string) (map[string]workspaceFile, error) {
	files := map[string]workspaceFile{}
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
		files[filepath.ToSlash(relative)] = workspaceFile{bytes: data, mode: info.Mode()}
		return nil
	})
	return files, err
}

func applyPlannedEdits(root string, edits []SourceEdit, verifyBefore bool) error {
	for _, edit := range edits {
		path, err := confinedPath(root, edit.Path)
		if err != nil {
			return err
		}
		info, statErr := os.Lstat(path)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if exists && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path escape through symlink: %s", edit.Path)
		}
		if verifyBefore {
			if exists != edit.BeforeExists {
				return fmt.Errorf("revision_conflict: %s existence changed", edit.Path)
			}
			if exists {
				before, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if byteDigest(before) != edit.BeforeDigest {
					return fmt.Errorf("revision_conflict: %s changed", edit.Path)
				}
			}
		}
		if !edit.AfterExists {
			if exists {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(edit.Mode).Perm()
		if mode == 0 {
			mode = 0o644
		}
		temporary := path + ".scenery-plan-stage"
		if err := os.WriteFile(temporary, edit.After, mode); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return err
		}
	}
	return nil
}

func commitPlannedEdits(root string, edits []SourceEdit, receiptPath string) (rollback func(), finalize func(), err error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	if err := workspacetx.RecoverOrReject(absoluteRoot, workspacetx.NormalRead); err != nil {
		return nil, nil, err
	}
	transactionRoot := filepath.Join(absoluteRoot, ".scenery", "transactions")
	if err := os.MkdirAll(transactionRoot, 0o755); err != nil {
		return nil, nil, err
	}
	if err := scn.RejectPathSymlinks(absoluteRoot, transactionRoot); err != nil {
		return nil, nil, err
	}
	identity := byteDigest([]byte(fmt.Sprintf("%d\x00%d\x00%v", os.Getpid(), time.Now().UnixNano(), edits)))
	identity = strings.TrimPrefix(identity, "sha256:")[:24]
	transactionDir := filepath.Join(transactionRoot, "change-"+identity)
	lockPath := filepath.Join(transactionRoot, "change.lock")
	journalPath := filepath.Join(transactionRoot, "change-apply.json")
	lock, journal := workspacetx.NewArtifacts(transactionDir, receiptPath)
	lockBytes, _ := json.Marshal(lock)
	lockFile, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("failed_precondition: workspace change transaction is active: %w", err)
	}
	lockOwned := true
	releaseLock := func() {
		if lockOwned {
			_ = os.Remove(lockPath)
			lockOwned = false
		}
	}
	if _, err := lockFile.Write(append(lockBytes, '\n')); err == nil {
		err = lockFile.Sync()
	}
	closeErr := lockFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		releaseLock()
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(transactionDir, "staged"), 0o700); err != nil {
		releaseLock()
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(transactionDir, "backups"), 0o700); err != nil {
		_ = os.RemoveAll(transactionDir)
		releaseLock()
		return nil, nil, err
	}
	ordered := append([]SourceEdit(nil), edits...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	for index, edit := range ordered {
		path, pathErr := confinedPath(absoluteRoot, edit.Path)
		if pathErr != nil {
			_ = os.RemoveAll(transactionDir)
			releaseLock()
			return nil, nil, pathErr
		}
		info, statErr := os.Lstat(path)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			_ = os.RemoveAll(transactionDir)
			releaseLock()
			return nil, nil, statErr
		}
		if exists && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			_ = os.RemoveAll(transactionDir)
			releaseLock()
			return nil, nil, fmt.Errorf("planned target is not a regular file: %s", edit.Path)
		}
		if exists != edit.BeforeExists {
			_ = os.RemoveAll(transactionDir)
			releaseLock()
			return nil, nil, fmt.Errorf("revision_conflict: %s existence changed", edit.Path)
		}
		if exists {
			before, readErr := os.ReadFile(path)
			if readErr != nil {
				_ = os.RemoveAll(transactionDir)
				releaseLock()
				return nil, nil, readErr
			}
			if byteDigest(before) != edit.BeforeDigest {
				_ = os.RemoveAll(transactionDir)
				releaseLock()
				return nil, nil, fmt.Errorf("revision_conflict: %s changed", edit.Path)
			}
		}
		stage := filepath.Join(transactionDir, "staged", fmt.Sprintf("%06d", index))
		backup := filepath.Join(transactionDir, "backups", fmt.Sprintf("%06d", index))
		if edit.AfterExists {
			mode := os.FileMode(edit.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			if writeErr := writeSyncedFile(stage, edit.After, mode); writeErr != nil {
				_ = os.RemoveAll(transactionDir)
				releaseLock()
				return nil, nil, writeErr
			}
		}
		journal.Entries = append(journal.Entries, workspacetx.Entry{
			Path: edit.Path, Stage: stage, Backup: backup, BeforeDigest: edit.BeforeDigest,
			AfterDigest: byteDigest(edit.After), BeforeExists: edit.BeforeExists, AfterExists: edit.AfterExists,
		})
	}
	if err := writeSyncedJSON(journalPath, journal, 0o600); err != nil {
		_ = os.RemoveAll(transactionDir)
		releaseLock()
		return nil, nil, err
	}
	rollbackCommitted := func() { _ = workspacetx.ForceRecover(absoluteRoot) }
	for index, edit := range ordered {
		path, _ := confinedPath(absoluteRoot, edit.Path)
		entry := journal.Entries[index]
		if edit.BeforeExists {
			if renameErr := os.Rename(path, entry.Backup); renameErr != nil {
				rollbackCommitted()
				return nil, nil, renameErr
			}
		}
		if edit.AfterExists {
			if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
				rollbackCommitted()
				return nil, nil, mkdirErr
			}
			if renameErr := os.Rename(entry.Stage, path); renameErr != nil {
				rollbackCommitted()
				return nil, nil, renameErr
			}
		}
	}
	rollback = rollbackCommitted
	finalize = func() {
		_ = os.RemoveAll(transactionDir)
		_ = os.Remove(journalPath)
		releaseLock()
	}
	return rollback, finalize, nil
}

func recoverInterruptedChangeTransaction(root string, force bool) error {
	if force {
		return workspacetx.ForceRecover(root)
	}
	return workspacetx.RecoverOrReject(root, workspacetx.NormalRead)
}

func recoverInterruptedChangeTransactionWithAccess(root string, force, allowCurrentOwner bool) error {
	if force {
		return workspacetx.ForceRecover(root)
	}
	mode := workspacetx.NormalRead
	if allowCurrentOwner {
		mode = workspacetx.CurrentOwnerRead
	}
	return workspacetx.RecoverOrReject(root, mode)
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func writeSyncedJSON(path string, value any, mode os.FileMode) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	_ = os.Remove(temporary)
	if err := writeSyncedFile(temporary, append(encoded, '\n'), mode); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
