package snapshotarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
	"scenery.sh/internal/atomicfile"
)

// Materialization is a disposable private source, not a shared live blob pool.
// Its exclusive file lease covers extraction, every visitor and eviction. The
// source Reader must remain open. Visitors must not retain payload descriptors.
type Materialization struct {
	mu      sync.Mutex
	archive *Reader
	root    *os.Root
	lease   *os.File
	path    string
	closed  bool
}

func (r *Reader) Materialize(ctx context.Context) (_ *Materialization, returnErr error) {
	path, err := os.MkdirTemp("", "scenery-snapshot-"+r.SHA256+"-")
	if err != nil {
		return nil, err
	}
	m := &Materialization{archive: r, path: path}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, m.Close())
		}
	}()
	m.root, err = os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	m.lease, err = m.root.OpenFile("source.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(m.lease.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, err
	}
	if r.Manifest.Storage != nil {
		for _, store := range r.Manifest.Storage.Stores {
			if err := r.VisitStore(ctx, store.Name, func(object Object, body io.Reader) error {
				name := PayloadPath(object)
				if err := m.root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
					return err
				}
				f, err := m.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					return err
				}
				err = copyVerified(ctx, f, body, object.SizeBytes, object.SHA256)
				return errors.Join(err, f.Close())
			}); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// No materialization survives this process by contract. The marker publishes
	// extraction completeness; target publication owns payload durability.
	if err := atomicfile.WriteRoot(m.root, "ready", []byte(r.SHA256+"\n"), 0o600, atomicfile.Options{}); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Materialization) VisitStore(ctx context.Context, name string, visit func(Object, io.Reader) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("snapshot materialization is closed")
	}
	ready, err := m.root.ReadFile("ready")
	if err != nil {
		return err
	}
	if string(ready) != m.archive.SHA256+"\n" {
		return fmt.Errorf("snapshot materialization identity differs")
	}
	return m.archive.VisitStore(ctx, name, func(object Object, _ io.Reader) error {
		f, err := m.root.OpenFile(PayloadPath(object), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		err = visit(object, f)
		return errors.Join(err, f.Close())
	})
}

// Close evicts only this materialization while its source lease is still held.
func (m *Materialization) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var err error
	if m.root != nil {
		err = removeMaterializationContents(m.root)
	}
	if m.root != nil {
		err = errors.Join(err, m.root.Close())
	}
	if m.lease != nil {
		err = errors.Join(err, m.lease.Close())
	}
	if removeErr := os.Remove(m.path); !errors.Is(removeErr, os.ErrNotExist) {
		err = errors.Join(err, removeErr)
	}
	return err
}

func removeMaterializationContents(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	for {
		names, readErr := dir.Readdirnames(64)
		for _, name := range names {
			if removeErr := root.RemoveAll(name); removeErr != nil {
				return errors.Join(removeErr, dir.Close())
			}
		}
		if errors.Is(readErr, io.EOF) {
			return dir.Close()
		}
		if readErr != nil {
			return errors.Join(readErr, dir.Close())
		}
	}
}
