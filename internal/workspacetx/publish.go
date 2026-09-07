package workspacetx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/scn"
)

// File is one workspace-relative generated output. The caller must establish
// artifact ownership before returning it from the publication callback.
type File struct {
	Path   string
	Bytes  []byte
	Remove bool
}

// Publish serializes preparation and publication on the existing workspace
// transaction boundary. The callback may compile using CurrentOwnerRead.
// Failed or interrupted publication is recovered by the ordinary source reader.
func Publish(root string, prepare func() ([]File, error)) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	transactionRoot := filepath.Join(root, ".scenery", "transactions")
	if err := scn.RejectPathSymlinks(root, transactionRoot); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(transactionRoot, 0o700); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(transactionRoot, "change-")
	if err != nil {
		return err
	}
	lockPath := filepath.Join(transactionRoot, "change.lock")
	journalPath := filepath.Join(transactionRoot, "change-apply.json")
	lock, journal := NewArtifacts(directory, filepath.Join(directory, "committed"))
	lockBytes, err := json.Marshal(lock)
	if err != nil {
		_ = os.RemoveAll(directory)
		return err
	}
	// Link a complete, synced lock record instead of exposing an empty O_EXCL
	// file to concurrent readers or leaving one behind after a process crash.
	preparedLock := filepath.Join(directory, "lock")
	if err := atomicfile.Write(preparedLock, lockBytes, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		_ = os.RemoveAll(directory)
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = RecoverOrReject(root, NormalRead)
		var active *activeTransactionError
		if err != nil && !errors.As(err, &active) {
			_ = os.RemoveAll(directory)
			return err
		}
		if err == nil {
			err = os.Link(preparedLock, lockPath)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrExist) {
				_ = os.RemoveAll(directory)
				return err
			}
		}
		if time.Now().After(deadline) {
			_ = os.RemoveAll(directory)
			return fmt.Errorf("failed_precondition: timed out waiting for the workspace transaction owner; retry after the active operation completes")
		}
		time.Sleep(10 * time.Millisecond)
	}
	journalWritten := false
	defer func() {
		if !journalWritten {
			_ = os.RemoveAll(directory)
			_ = os.Remove(lockPath)
		}
	}()
	files, err := prepare()
	if err != nil {
		return err
	}
	files = append([]File(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for index, file := range files {
		if index > 0 && files[index-1].Path == file.Path {
			return fmt.Errorf("failed_precondition: duplicate generated output %s", file.Path)
		}
		path, err := confinedPath(root, file.Path)
		if err != nil {
			return err
		}
		if err := scn.RejectPathSymlinks(root, path); err != nil && !os.IsNotExist(err) {
			return err
		}
		info, statErr := os.Lstat(path)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		var before []byte
		if exists {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("failed_precondition: generated output is not a regular file: %s", file.Path)
			}
			before, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		if file.Remove && !exists || !file.Remove && exists && bytes.Equal(before, file.Bytes) {
			continue
		}
		entryIndex := len(journal.Entries)
		entry := Entry{
			Path: file.Path, Stage: filepath.Join(directory, "staged", fmt.Sprintf("%06d", entryIndex)),
			Backup:       filepath.Join(directory, "backups", fmt.Sprintf("%06d", entryIndex)),
			BeforeExists: exists, BeforeDigest: digest(before), AfterExists: !file.Remove, AfterDigest: digest(file.Bytes),
		}
		if err := os.MkdirAll(filepath.Dir(entry.Backup), 0o700); err != nil {
			return err
		}
		if !file.Remove {
			if err := os.MkdirAll(filepath.Dir(entry.Stage), 0o700); err != nil {
				return err
			}
			if err := atomicfile.Write(entry.Stage, file.Bytes, 0o644, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
				return err
			}
		}
		journal.Entries = append(journal.Entries, entry)
	}
	if len(journal.Entries) == 0 {
		return nil
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(journalPath, encoded, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return err
	}
	journalWritten = true
	rollback := func(cause error) error {
		if err := ForceRecover(root); err != nil {
			return fmt.Errorf("%w; transaction recovery requires attention: %v", cause, err)
		}
		return cause
	}
	for _, entry := range journal.Entries {
		path := filepath.Join(root, entry.Path)
		if err := scn.RejectPathSymlinks(root, path); err != nil && !os.IsNotExist(err) {
			return rollback(err)
		}
		if entry.BeforeExists {
			current, err := regularFileBytes(path, entry.Path)
			if err != nil || digest(current) != entry.BeforeDigest {
				return rollback(fmt.Errorf("failed_precondition: generated output changed during publication: %s", entry.Path))
			}
			if err := os.Rename(path, entry.Backup); err != nil {
				return rollback(err)
			}
		} else if pathExists(path) {
			return rollback(fmt.Errorf("failed_precondition: generated output appeared during publication: %s", entry.Path))
		}
		if entry.AfterExists {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return rollback(err)
			}
			if err := os.Rename(entry.Stage, path); err != nil {
				return rollback(err)
			}
		}
	}
	if err := atomicfile.Write(journal.Receipt, []byte("committed\n"), 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return rollback(err)
	}
	// Remove the journal before its committed marker/backups. Recovery must
	// never interpret a committed set as uncommitted after partial cleanup.
	if err := os.Remove(journalPath); err != nil {
		return err
	}
	journalWritten = false
	return nil
}
