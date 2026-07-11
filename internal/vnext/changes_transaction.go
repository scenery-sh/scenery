package vnext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
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
			return nil
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

type changeTransactionLock struct {
	APIVersion     string           `json:"api_version"`
	Owner          localagent.Owner `json:"owner"`
	TransactionDir string           `json:"transaction_dir"`
}

type changeTransactionEntry struct {
	Path         string `json:"path"`
	Stage        string `json:"stage"`
	Backup       string `json:"backup"`
	BeforeDigest string `json:"before_digest"`
	AfterDigest  string `json:"after_digest,omitempty"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
}

type changeTransactionJournal struct {
	APIVersion string                   `json:"api_version"`
	Owner      localagent.Owner         `json:"owner"`
	Receipt    string                   `json:"receipt,omitempty"`
	Directory  string                   `json:"directory"`
	Entries    []changeTransactionEntry `json:"entries"`
}

func commitPlannedEdits(root string, edits []SourceEdit, receiptPath string) (rollback func(), finalize func(), err error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	if err := recoverInterruptedChangeTransaction(absoluteRoot, false); err != nil {
		return nil, nil, err
	}
	transactionRoot := filepath.Join(absoluteRoot, ".scenery", "transactions")
	if err := os.MkdirAll(transactionRoot, 0o755); err != nil {
		return nil, nil, err
	}
	if err := rejectPathSymlinks(absoluteRoot, transactionRoot); err != nil {
		return nil, nil, err
	}
	identity := byteDigest([]byte(fmt.Sprintf("%d\x00%d\x00%v", os.Getpid(), time.Now().UnixNano(), edits)))
	identity = strings.TrimPrefix(identity, "sha256:")[:24]
	transactionDir := filepath.Join(transactionRoot, "change-"+identity)
	lockPath := filepath.Join(transactionRoot, "change.lock")
	journalPath := filepath.Join(transactionRoot, "change-apply.json")
	owner := localagent.CurrentOwner("vnext-change-transaction")
	lock := changeTransactionLock{APIVersion: "scenery.change-transaction-lock/v1", Owner: owner, TransactionDir: transactionDir}
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
	journal := changeTransactionJournal{APIVersion: "scenery.change-transaction/v1", Owner: owner, Receipt: receiptPath, Directory: transactionDir}
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
		journal.Entries = append(journal.Entries, changeTransactionEntry{
			Path: edit.Path, Stage: stage, Backup: backup, BeforeDigest: edit.BeforeDigest,
			AfterDigest: byteDigest(edit.After), BeforeExists: edit.BeforeExists, AfterExists: edit.AfterExists,
		})
	}
	if err := writeSyncedJSON(journalPath, journal, 0o600); err != nil {
		_ = os.RemoveAll(transactionDir)
		releaseLock()
		return nil, nil, err
	}
	rollbackCommitted := func() { _ = recoverInterruptedChangeTransaction(absoluteRoot, true) }
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
	return recoverInterruptedChangeTransactionWithAccess(root, force, false)
}

func recoverInterruptedChangeTransactionWithAccess(root string, force, allowCurrentOwner bool) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	transactionRoot := filepath.Join(absoluteRoot, ".scenery", "transactions")
	if info, err := os.Lstat(transactionRoot); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("failed_precondition: workspace transaction root is invalid")
		}
		if err := rejectPathSymlinks(absoluteRoot, transactionRoot); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	lockPath := filepath.Join(transactionRoot, "change.lock")
	journalPath := filepath.Join(transactionRoot, "change-apply.json")
	lock, lockExists, err := readChangeTransactionLock(lockPath)
	if err != nil {
		return err
	}
	if lockExists && !force {
		if transactionOwnerIsCurrent(lock.Owner) {
			if !allowCurrentOwner {
				return fmt.Errorf("failed_precondition: workspace change transaction is active in process %d", lock.Owner.PID)
			}
		} else if localagent.VerifyOwner(lock.Owner) == nil {
			return fmt.Errorf("failed_precondition: workspace change transaction is active in process %d", lock.Owner.PID)
		}
	}
	journalInfo, journalStatErr := os.Lstat(journalPath)
	if journalStatErr == nil && (journalInfo.Mode()&os.ModeSymlink != 0 || !journalInfo.Mode().IsRegular()) {
		return fmt.Errorf("failed_precondition: invalid workspace change transaction journal")
	}
	if journalStatErr != nil && !os.IsNotExist(journalStatErr) {
		return journalStatErr
	}
	encoded, err := os.ReadFile(journalPath)
	if os.IsNotExist(err) {
		if lockExists {
			if !force && transactionOwnerIsCurrent(lock.Owner) && allowCurrentOwner {
				return nil
			}
			if err := validateChangeTransactionDirectory(transactionRoot, lock.TransactionDir); err != nil {
				return err
			}
			_ = os.RemoveAll(lock.TransactionDir)
			_ = os.Remove(lockPath)
		}
		return nil
	}
	if err != nil {
		return err
	}
	var journal changeTransactionJournal
	if err := json.Unmarshal(encoded, &journal); err != nil || journal.APIVersion != "scenery.change-transaction/v1" {
		return fmt.Errorf("failed_precondition: invalid workspace change transaction journal")
	}
	if err := validateChangeTransactionMetadata(absoluteRoot, transactionRoot, lock, lockExists, journal); err != nil {
		return err
	}
	if !force {
		if transactionOwnerIsCurrent(journal.Owner) {
			if allowCurrentOwner {
				return nil
			}
			return fmt.Errorf("failed_precondition: workspace change transaction is active in process %d", journal.Owner.PID)
		}
		if localagent.VerifyOwner(journal.Owner) == nil {
			return fmt.Errorf("failed_precondition: workspace change transaction is active in process %d", journal.Owner.PID)
		}
	}
	committed := journal.Receipt != "" && pathExists(journal.Receipt)
	if !committed {
		for index := len(journal.Entries) - 1; index >= 0; index-- {
			entry := journal.Entries[index]
			target, pathErr := confinedPath(absoluteRoot, entry.Path)
			if pathErr != nil {
				return pathErr
			}
			if pathExists(entry.Backup) {
				if err := verifyInterruptedTransactionTarget(target, entry); err != nil {
					return err
				}
				if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				if err := os.Rename(entry.Backup, target); err != nil {
					return err
				}
			} else if entry.BeforeExists {
				if !pathExists(target) {
					return fmt.Errorf("failed_precondition: interrupted transaction lost both target and backup for %s", entry.Path)
				}
				info, statErr := os.Lstat(target)
				if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("failed_precondition: interrupted transaction target %s is not a regular file", entry.Path)
				}
				current, readErr := os.ReadFile(target)
				if readErr != nil || byteDigest(current) != entry.BeforeDigest {
					return fmt.Errorf("failed_precondition: interrupted transaction target %s was changed externally", entry.Path)
				}
			} else if pathExists(target) {
				info, statErr := os.Lstat(target)
				if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("failed_precondition: interrupted transaction target %s is not a regular file", entry.Path)
				}
				current, readErr := os.ReadFile(target)
				if readErr != nil {
					return readErr
				}
				if byteDigest(current) != entry.AfterDigest {
					return fmt.Errorf("failed_precondition: interrupted transaction target %s was changed externally", entry.Path)
				}
				if err := os.Remove(target); err != nil {
					return err
				}
			}
		}
	}
	_ = os.RemoveAll(journal.Directory)
	_ = os.Remove(journalPath)
	_ = os.Remove(lockPath)
	return nil
}

func readChangeTransactionLock(path string) (changeTransactionLock, bool, error) {
	info, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		return changeTransactionLock{}, false, nil
	}
	if statErr != nil {
		return changeTransactionLock{}, false, statErr
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return changeTransactionLock{}, false, fmt.Errorf("failed_precondition: invalid workspace change transaction lock")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return changeTransactionLock{}, false, err
	}
	var lock changeTransactionLock
	if err := json.Unmarshal(encoded, &lock); err != nil || lock.APIVersion != "scenery.change-transaction-lock/v1" {
		return changeTransactionLock{}, false, fmt.Errorf("failed_precondition: invalid workspace change transaction lock")
	}
	return lock, true, nil
}

func validateChangeTransactionMetadata(root, transactionRoot string, lock changeTransactionLock, lockExists bool, journal changeTransactionJournal) error {
	if err := validateChangeTransactionDirectory(transactionRoot, journal.Directory); err != nil {
		return err
	}
	if lockExists {
		if err := validateChangeTransactionDirectory(transactionRoot, lock.TransactionDir); err != nil {
			return err
		}
		if filepath.Clean(lock.TransactionDir) != filepath.Clean(journal.Directory) {
			return fmt.Errorf("failed_precondition: workspace transaction lock and journal disagree")
		}
	}
	if journal.Receipt != "" {
		receipt, err := filepath.Abs(journal.Receipt)
		if err != nil || !pathWithin(root, receipt) || filepath.Clean(journal.Receipt) != receipt {
			return fmt.Errorf("failed_precondition: workspace transaction receipt escapes the app root")
		}
		if pathExists(receipt) {
			if err := rejectPathSymlinks(root, receipt); err != nil {
				return fmt.Errorf("failed_precondition: workspace transaction receipt is invalid: %w", err)
			}
		}
	}
	for index, entry := range journal.Entries {
		expectedStage := filepath.Join(journal.Directory, "staged", fmt.Sprintf("%06d", index))
		expectedBackup := filepath.Join(journal.Directory, "backups", fmt.Sprintf("%06d", index))
		if filepath.Clean(entry.Stage) != expectedStage || filepath.Clean(entry.Backup) != expectedBackup || !pathWithin(journal.Directory, entry.Stage) || !pathWithin(journal.Directory, entry.Backup) {
			return fmt.Errorf("failed_precondition: workspace transaction artifact path is invalid")
		}
		for _, artifact := range []string{entry.Stage, entry.Backup} {
			if err := rejectPathSymlinks(transactionRoot, filepath.Dir(artifact)); err != nil {
				return fmt.Errorf("failed_precondition: workspace transaction artifact path is invalid: %w", err)
			}
			if info, err := os.Lstat(artifact); err == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("failed_precondition: workspace transaction artifact is invalid")
				}
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if _, err := confinedPath(root, entry.Path); err != nil {
			return fmt.Errorf("failed_precondition: workspace transaction target is invalid: %w", err)
		}
	}
	return nil
}

func verifyInterruptedTransactionTarget(target string, entry changeTransactionEntry) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		if entry.AfterExists {
			return fmt.Errorf("failed_precondition: interrupted transaction target %s disappeared", entry.Path)
		}
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("failed_precondition: interrupted transaction target %s is not a regular file", entry.Path)
	}
	if !entry.AfterExists {
		return fmt.Errorf("failed_precondition: interrupted transaction target %s was created externally", entry.Path)
	}
	current, err := os.ReadFile(target)
	if err != nil || byteDigest(current) != entry.AfterDigest {
		return fmt.Errorf("failed_precondition: interrupted transaction target %s was changed externally", entry.Path)
	}
	return nil
}

func atomicWriteSynced(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	_ = os.Remove(temporary)
	if err := writeSyncedFile(temporary, data, mode); err != nil {
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

func validateChangeTransactionDirectory(transactionRoot, directory string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil || filepath.Clean(directory) != absolute || filepath.Dir(absolute) != filepath.Clean(transactionRoot) || !strings.HasPrefix(filepath.Base(absolute), "change-") || !pathWithin(transactionRoot, absolute) {
		return fmt.Errorf("failed_precondition: workspace transaction directory is invalid")
	}
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("failed_precondition: workspace transaction directory is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func transactionOwnerIsCurrent(owner localagent.Owner) bool {
	if owner.PID != os.Getpid() || localagent.VerifyOwner(owner) != nil {
		return false
	}
	current := localagent.CurrentOwner("vnext-change-transaction")
	return owner.StartedAt == current.StartedAt && owner.Exe == current.Exe && owner.CmdlineHash == current.CmdlineHash
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
func confinedPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
		return "", fmt.Errorf("path escape")
	}
	absoluteRoot, _ := filepath.Abs(root)
	target := filepath.Join(absoluteRoot, filepath.FromSlash(relative))
	if !strings.HasPrefix(target, absoluteRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escape")
	}
	current := absoluteRoot
	parts := strings.Split(filepath.Clean(filepath.FromSlash(relative)), string(filepath.Separator))
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
func byteDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
