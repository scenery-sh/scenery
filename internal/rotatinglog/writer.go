// Package rotatinglog bounds a detached supervisor's local event history.
package rotatinglog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

const (
	SegmentBytes = 8 << 20
	Backups      = 3
	Sessions     = 4
)

// Writer serializes writes and keeps one active file plus bounded backups.
// A write may cross segments; readers concatenate retained segments oldest first.
type Writer struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	limit   int64
	backups int
	err     error
}

func Open(path string) (*Writer, error) { return open(path, SegmentBytes, Backups) }

func open(path string, limit int64, backups int) (*Writer, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Writer{path: path, file: file, size: info.Size(), limit: limit, backups: backups}, nil
}

func (w *Writer) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(data) > 0 {
		if w.size >= w.limit {
			if err := w.rotate(); err != nil {
				w.err = err
				return written, err
			}
		}
		count := min(len(data), int(w.limit-w.size))
		n, err := w.file.Write(data[:count])
		written += n
		w.size += int64(n)
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n != count {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (w *Writer) rotate() error {
	for n := w.backups; n > 0; n-- {
		from := w.path
		if n > 1 {
			from += "." + strconv.Itoa(n-1)
		}
		if err := os.Rename(from, w.path+"."+strconv.Itoa(n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_ = w.file.Close()
	w.file, w.size = file, 0
	return nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// Segments names retained files in stream order. Open them before reading so
// a concurrent rename never changes which inode a reader consumes.
func Segments(path string) []string {
	paths := make([]string, 0, Backups+1)
	for n := Backups; n > 0; n-- {
		paths = append(paths, path+"."+strconv.Itoa(n))
	}
	return append(paths, path)
}

// Prune removes older sessions only after the caller owns the worktree's live
// lock. Each directory belongs to that one worktree; the active path is kept.
func Prune(active string) error {
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(active), "*.log"))
	if err != nil {
		return err
	}
	retained := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != active {
			retained = append(retained, path)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(retained)))
	budget := int64((Sessions - 1) * (Backups + 1) * SegmentBytes)
	for index, path := range retained {
		var size int64
		for _, segment := range Segments(path) {
			info, err := os.Stat(segment)
			if err == nil {
				size += info.Size()
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if index < Sessions-1 && size <= budget {
			budget -= size
			continue
		}
		for _, segment := range Segments(path) {
			if err := os.Remove(segment); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("prune supervisor log: %w", err)
			}
		}
	}
	return nil
}
