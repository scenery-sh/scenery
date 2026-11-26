//go:build unix

package jsbuild

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var cacheMu sync.Mutex

// withCacheLock serializes reads, cache-index publications, and binary pruning
// across concurrent goroutines and external build/prune CLI processes.
func withCacheLock(privateRoot string, fn func() error) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	file, err := os.OpenFile(filepath.Join(privateRoot, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return fn()
}
