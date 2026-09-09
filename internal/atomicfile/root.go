package atomicfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// PublicationError means replacement happened but its durability could not be
// confirmed. Callers must retain both old and new referenced resources.
type PublicationError struct{ Err error }

func (e *PublicationError) Error() string {
	return "file replacement outcome is uncertain: " + e.Err.Error()
}
func (e *PublicationError) Unwrap() error { return e.Err }

// WriteRoot replaces an existing-root-relative file. Unlike Write it does not
// create parents; the owner must establish and synchronize them first.
func WriteRoot(root *os.Root, name string, data []byte, perm os.FileMode, opts Options) error {
	return writeRoot(root, name, data, perm, opts, func(f *os.File) error { return f.Sync() })
}

func writeRoot(root *os.Root, name string, data []byte, perm os.FileMode, opts Options, syncFile func(*os.File) error) error {
	return replaceRoot(root, name, perm, opts, syncFile, func(f *os.File) error { _, err := f.Write(data); return err })
}

// CopyRoot stages a bounded-memory transfer and validates its complete length
// before replacing the destination. A failed transfer preserves the old file.
func CopyRoot(root *os.Root, name string, src io.Reader, length int64, perm os.FileMode, opts Options) error {
	return replaceRoot(root, name, perm, opts, func(f *os.File) error { return f.Sync() }, func(f *os.File) error {
		if length < 0 {
			return fmt.Errorf("negative expected transfer length")
		}
		n, err := io.Copy(f, io.LimitReader(src, length))
		if err != nil {
			return err
		}
		if n != length {
			return io.ErrUnexpectedEOF
		}
		var extra [1]byte
		nExtra, err := src.Read(extra[:])
		if nExtra != 0 {
			return fmt.Errorf("transfer exceeds expected length")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if err == nil {
			return io.ErrNoProgress
		}
		return nil
	})
}

func replaceRoot(root *os.Root, name string, perm os.FileMode, opts Options, syncFile func(*os.File) error, write func(*os.File) error) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmpName := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+".tmp-"+hex.EncodeToString(nonce[:]))
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmpName) }()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if opts.SyncFile {
		if err := syncFile(tmp); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := root.Rename(tmpName, name); err != nil {
		return err
	}
	if opts.SyncDir {
		parent, err := root.Open(filepath.Dir(name))
		if err != nil {
			return &PublicationError{Err: err}
		}
		err = syncFile(parent)
		closeErr := parent.Close()
		if err != nil {
			return &PublicationError{Err: err}
		}
		if closeErr != nil {
			return &PublicationError{Err: fmt.Errorf("close replacement directory: %w", closeErr)}
		}
	}
	return nil
}
