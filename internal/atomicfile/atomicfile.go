// Package atomicfile is the single implementation of write-temp-then-rename
// file replacement. Every Scenery package that persists state atomically
// delegates here instead of hand-rolling its own temp-file mechanics, so
// permission handling, cleanup, and durability options stay consistent.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Options selects durability guarantees beyond the atomic rename itself.
type Options struct {
	// SyncFile fsyncs the temp file before the rename, so the new contents
	// survive power loss once Write returns.
	SyncFile bool
	// SyncDir fsyncs the parent directory after the rename, so the rename
	// itself survives power loss once Write returns.
	SyncDir bool
}

// Write atomically replaces path with data: it creates the parent directory,
// writes a uniquely named temp file beside path, applies perm, and renames it
// over path. The temp file is removed on any failure.
func Write(path string, data []byte, perm os.FileMode, opts Options) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if opts.SyncFile {
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if opts.SyncDir {
		parent, err := os.Open(dir)
		if err != nil {
			return err
		}
		if err := parent.Sync(); err != nil {
			_ = parent.Close()
			return err
		}
		return parent.Close()
	}
	return nil
}
