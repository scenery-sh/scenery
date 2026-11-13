package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

type SourceEdit struct {
	Path         string `json:"path"`
	BeforeDigest string `json:"before_digest"`
	After        []byte `json:"after"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	Mode         uint32 `json:"mode,omitempty"`
}

func byteDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func confinedPath(root, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escape")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absoluteRoot, clean)
	current := absoluteRoot
	parts := strings.Split(clean, string(filepath.Separator))
	for _, part := range parts[:max(0, len(parts)-1)] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path escape through symlink: %s", relative)
		}
	}
	return target, nil
}

func commitPlannedEdits(root string, edits []SourceEdit) (rollback func(), finalize func(), err error) {
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
	lock, journal := workspacetx.NewArtifacts(transactionDir, "")
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
	if _, err = lockFile.Write(append(lockBytes, '\n')); err == nil {
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
