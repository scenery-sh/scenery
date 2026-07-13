package workspacetx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
)

type ReadMode uint8

const (
	NormalRead ReadMode = iota
	CurrentOwnerRead
)

func RecoverOrReject(root string, mode ReadMode) error {
	return recover(root, false, mode == CurrentOwnerRead)
}

func ForceRecover(root string) error { return recover(root, true, false) }

func recover(root string, force, allowCurrentOwner bool) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	transactionRoot := filepath.Join(absoluteRoot, ".scenery", "transactions")
	if info, err := os.Lstat(transactionRoot); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("failed_precondition: workspace transaction root is invalid")
		}
		if err := scn.RejectPathSymlinks(absoluteRoot, transactionRoot); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	lockPath := filepath.Join(transactionRoot, "change.lock")
	journalPath := filepath.Join(transactionRoot, "change-apply.json")
	lock, lockExists, err := readLock(lockPath)
	if err != nil {
		return err
	}
	if lockExists && !force {
		if ownerIsCurrent(lock.Owner) {
			if !allowCurrentOwner {
				return activeOwnerError(lock.Owner.PID)
			}
		} else if verifyOwner(lock.Owner) == nil {
			return activeOwnerError(lock.Owner.PID)
		}
	}
	if info, err := os.Lstat(journalPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("failed_precondition: invalid workspace change transaction journal")
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	encoded, err := os.ReadFile(journalPath)
	if os.IsNotExist(err) {
		if lockExists {
			if !force && ownerIsCurrent(lock.Owner) && allowCurrentOwner {
				return nil
			}
			if err := validateDirectory(transactionRoot, lock.TransactionDir); err != nil {
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
	if hasLegacyIdentity(encoded, "scenery.change-transaction/v1") {
		return legacyStateError(journalPath)
	}
	var journal Journal
	if decodeExact(encoded, &journal) != nil || machine.ValidateArtifactIdentity(journal.ArtifactIdentity, journalKind, journalDescriptor, "re-plan") != nil {
		return fmt.Errorf("failed_precondition: invalid workspace change transaction journal")
	}
	if err := validateMetadata(absoluteRoot, transactionRoot, lock, lockExists, journal); err != nil {
		return err
	}
	if !force {
		if ownerIsCurrent(journal.Owner) {
			if allowCurrentOwner {
				return nil
			}
			return activeOwnerError(journal.Owner.PID)
		}
		if verifyOwner(journal.Owner) == nil {
			return activeOwnerError(journal.Owner.PID)
		}
	}
	if journal.Receipt == "" || !pathExists(journal.Receipt) {
		for index := len(journal.Entries) - 1; index >= 0; index-- {
			entry := journal.Entries[index]
			target, err := confinedPath(absoluteRoot, entry.Path)
			if err != nil {
				return err
			}
			if pathExists(entry.Backup) {
				if err := verifyTarget(target, entry); err != nil {
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
				current, err := regularFileBytes(target, entry.Path)
				if err != nil || digest(current) != entry.BeforeDigest {
					return fmt.Errorf("failed_precondition: interrupted transaction target %s was changed externally", entry.Path)
				}
			} else if pathExists(target) {
				current, err := regularFileBytes(target, entry.Path)
				if err != nil {
					return err
				}
				if digest(current) != entry.AfterDigest {
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

func readLock(path string) (Lock, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Lock{}, false, nil
	}
	if err != nil {
		return Lock{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Lock{}, false, fmt.Errorf("failed_precondition: invalid workspace change transaction lock")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Lock{}, false, err
	}
	if hasLegacyIdentity(encoded, "scenery.change-transaction-lock/v1") {
		return Lock{}, false, legacyStateError(path)
	}
	var lock Lock
	if decodeExact(encoded, &lock) != nil || machine.ValidateArtifactIdentity(lock.ArtifactIdentity, lockKind, lockDescriptor, "retry") != nil {
		return Lock{}, false, fmt.Errorf("failed_precondition: invalid workspace change transaction lock")
	}
	return lock, true, nil
}

func validateMetadata(root, transactionRoot string, lock Lock, lockExists bool, journal Journal) error {
	if err := validateDirectory(transactionRoot, journal.Directory); err != nil {
		return err
	}
	if lockExists {
		if err := validateDirectory(transactionRoot, lock.TransactionDir); err != nil {
			return err
		}
		if filepath.Clean(lock.TransactionDir) != filepath.Clean(journal.Directory) {
			return fmt.Errorf("failed_precondition: workspace transaction lock and journal disagree")
		}
	}
	if journal.Receipt != "" {
		receipt, err := filepath.Abs(journal.Receipt)
		if err != nil || filepath.Clean(journal.Receipt) != receipt || !scn.PathWithin(root, receipt) {
			return fmt.Errorf("failed_precondition: workspace transaction receipt escapes the app root")
		}
		if pathExists(receipt) && scn.RejectPathSymlinks(root, receipt) != nil {
			return fmt.Errorf("failed_precondition: workspace transaction receipt is invalid")
		}
	}
	for index, entry := range journal.Entries {
		expectedStage := filepath.Join(journal.Directory, "staged", fmt.Sprintf("%06d", index))
		expectedBackup := filepath.Join(journal.Directory, "backups", fmt.Sprintf("%06d", index))
		if filepath.Clean(entry.Stage) != expectedStage || filepath.Clean(entry.Backup) != expectedBackup || !scn.PathWithin(journal.Directory, entry.Stage) || !scn.PathWithin(journal.Directory, entry.Backup) {
			return fmt.Errorf("failed_precondition: workspace transaction artifact path is invalid")
		}
		for _, artifact := range []string{entry.Stage, entry.Backup} {
			if err := scn.RejectPathSymlinks(transactionRoot, filepath.Dir(artifact)); err != nil {
				return fmt.Errorf("failed_precondition: workspace transaction artifact path is invalid: %w", err)
			}
			if info, err := os.Lstat(artifact); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
				return fmt.Errorf("failed_precondition: workspace transaction artifact is invalid")
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

func verifyTarget(target string, entry Entry) error {
	if !pathExists(target) {
		if entry.AfterExists {
			return fmt.Errorf("failed_precondition: interrupted transaction target %s disappeared", entry.Path)
		}
		return nil
	}
	current, err := regularFileBytes(target, entry.Path)
	if err != nil {
		return err
	}
	if !entry.AfterExists {
		return fmt.Errorf("failed_precondition: interrupted transaction target %s was created externally", entry.Path)
	}
	if digest(current) != entry.AfterDigest {
		return fmt.Errorf("failed_precondition: interrupted transaction target %s was changed externally", entry.Path)
	}
	return nil
}

func validateDirectory(root, directory string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil || filepath.Clean(directory) != absolute || filepath.Dir(absolute) != filepath.Clean(root) || !strings.HasPrefix(filepath.Base(absolute), "change-") || !scn.PathWithin(root, absolute) {
		return fmt.Errorf("failed_precondition: workspace transaction directory is invalid")
	}
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("failed_precondition: workspace transaction directory is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func confinedPath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", relative)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if !scn.PathWithin(root, path) {
		return "", fmt.Errorf("path escapes workspace: %s", relative)
	}
	return path, nil
}

func regularFileBytes(path, relative string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("failed_precondition: interrupted transaction target %s is not a regular file", relative)
	}
	return os.ReadFile(path)
}

func decodeExact(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func hasLegacyIdentity(encoded []byte, identity string) bool {
	var value struct {
		APIVersion string `json:"api_version"`
	}
	return json.Unmarshal(encoded, &value) == nil && value.APIVersion == identity
}

func legacyStateError(path string) error {
	return fmt.Errorf("failed_precondition: legacy change transaction recovery state at %s must be recovered with the previous Scenery binary before using this binary; no state was modified", filepath.ToSlash(path))
}

func activeOwnerError(pid int) error {
	return fmt.Errorf("failed_precondition: workspace change transaction is active in process %d", pid)
}

func pathExists(path string) bool { _, err := os.Lstat(path); return err == nil }

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
