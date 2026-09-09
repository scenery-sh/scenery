package stateupgrade

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

const (
	maxMetadataBytes = 64 << 10
	maxBackupBytes   = 256 << 20
)

type Store struct {
	path    string
	root    *os.Root
	write   func(string, []byte) error
	remove  func(string) error
	syncDir func(string) error
	options atomicfile.Options
}

func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("%w: selected state root is not canonical", ErrPrecondition)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := checkPrivate(info, true); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = r.Close()
		return nil, fmt.Errorf("%w: retained root changed while opening", ErrPrecondition)
	}
	s := &Store{path: path, root: r, options: atomicfile.Options{SyncFile: true, SyncDir: true}}
	s.syncDir = s.syncDirectory
	s.write = func(name string, data []byte) error {
		if err := s.checkParents(name); err != nil {
			return err
		}
		if info, err := r.Lstat(name); err == nil {
			if err := checkMetadataEntry(name, info); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return atomicfile.WriteRoot(r, name, data, 0o600, s.options)
	}
	s.remove = r.Remove
	return s, nil
}

func (s *Store) Close() error { return s.root.Close() }

// ReadMetadata reads an existing private artifact without allocating state.
func (s *Store) ReadMetadata(name string) ([]byte, error) {
	return s.read(name, maxMetadataBytes)
}

// CheckPending is an existence barrier, including for an unreadable marker.
// Only explicit transaction recovery may interpret or remove that marker.
func CheckPending(root string) error {
	_, err := os.Lstat(filepath.Join(root, PendingName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: cannot inspect the pending upgrade", ErrPrecondition)
	}
	return fmt.Errorf("%w: resume scenery worktree upgrade before ordinary access", ErrPrecondition)
}

func (s *Store) checkParents(name string) error {
	if !filepath.IsLocal(name) || filepath.Clean(name) != name || name == "." {
		return fmt.Errorf("%w: unsafe transaction path", ErrPrecondition)
	}
	parent := filepath.Dir(name)
	if parent == "." {
		return nil
	}
	current := ""
	for part := range strings.SplitSeq(parent, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := s.root.Lstat(current)
		if err != nil {
			return err
		}
		if err := checkProtected(info, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) read(name string, limit int64) ([]byte, error) {
	if err := s.checkParents(name); err != nil {
		return nil, err
	}
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkMetadataEntry(name, info); err != nil {
		return nil, err
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%w: retained metadata changed while opening", ErrPrecondition)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: retained metadata exceeds its size limit", ErrPrecondition)
	}
	return data, nil
}

func (s *Store) ensureBackup(name string, data []byte) error {
	for _, dir := range []string{backupRoot, filepath.Dir(name)} {
		if err := s.root.Mkdir(dir, 0o700); err == nil {
			if err := s.syncDir(filepath.Dir(dir)); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := s.root.Lstat(dir)
		if err != nil {
			return err
		}
		if err := checkPrivate(info, true); err != nil {
			return err
		}
	}
	old, err := s.read(name, maxBackupBytes)
	if err == nil {
		var saved backup
		if err := machine.DecodeArtifact(old, &saved, &saved.ArtifactIdentity, backupKind, backupShape, "resume with the matching upgrade CLI"); err != nil {
			return err
		}
		if !bytes.Equal(old, data) && !machine.ArtifactPayloadEqual(old, data) {
			return fmt.Errorf("%w: existing backup differs; it was not replaced", ErrPrecondition)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.write(name, data)
}

func (s *Store) syncDirectory(name string) error {
	f, err := s.root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func checkPrivate(info os.FileInfo, directory bool) error {
	if info == nil || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: retained state must be private, owned and non-symlink", ErrPrecondition)
	}
	return checkProtected(info, directory)
}

func checkMetadataEntry(name string, info os.FileInfo) error {
	if name == PendingName || strings.HasPrefix(name, backupRoot+string(filepath.Separator)) {
		return checkPrivate(info, false)
	}
	return checkProtected(info, false)
}

// The anchored root is owner-only. Existing control history may be readable
// beneath that root, but no descendant may be writable by another user.
func checkProtected(info os.FileInfo, directory bool) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || !owned(info) || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("%w: retained metadata must be owned, protected and non-symlink", ErrPrecondition)
	}
	return nil
}
