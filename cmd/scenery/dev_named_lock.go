package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	devLockOrderSubstrate       = 10
	devLockOrderBranchOperation = 15
	devLockOrderRegistry        = 20
)

// Lock ordering invariant:
//  1. Substrate locks serialize slow shared substrate startup.
//  2. Branch operation locks serialize shared database DDL.
//  3. Registry locks protect short branch-registry reads/writes only.
//
// Do not hold branches.lock while acquiring a substrate lock or starting a
// substrate. Long startup must happen under the substrate lock alone, database
// DDL under a branch operation lock, and branch lease metadata under a short
// registry lock. The process-local held-lock set below rejects same-process
// re-acquisition and lower-order inversions before the OS lock can block
// forever. Keep lock acquisition paths non-overlapping inside one process; the
// in-process ordering guard is intentionally conservative because the durable
// cross-process boundary is the OS file lock.
var (
	devLockRetryInterval = 50 * time.Millisecond
	devLockWarnAfter     = 2 * time.Second
	devLockWarnRepeat    = 15 * time.Second
	devLockTimeout       = 2 * time.Minute
	devLockWarnWriter    io.Writer

	devHeldLocks = struct {
		sync.Mutex
		byPath map[string]heldDevLock
	}{byPath: map[string]heldDevLock{}}
)

type heldDevLock struct {
	kind  string
	order int
}

func init() {
	devLockWarnWriter = os.Stderr
}

func acquireDevNamedLock(root, name, kind string, order int) (func(), error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(root, name)
	cleanPath := filepath.Clean(path)
	if err := checkDevLockOrder(cleanPath, kind, order); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	nextWarning := devLockWarnAfter
	for {
		err := tryLockDevFile(file)
		if err == nil {
			markDevLockHeld(cleanPath, kind, order)
			return func() {
				unmarkDevLockHeld(cleanPath)
				_ = unlockDevFile(file)
				_ = file.Close()
			}, nil
		}
		if !isDevFileLockBusy(err) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s at %s: %w", kind, cleanPath, err)
		}
		elapsed := time.Since(start)
		if elapsed >= nextWarning {
			if devLockWarnWriter != nil {
				_, _ = fmt.Fprintf(devLockWarnWriter, "waiting for %s lock at %s (%s elapsed)\n", kind, cleanPath, elapsed.Round(time.Second))
			}
			nextWarning += devLockWarnRepeat
		}
		if elapsed >= devLockTimeout {
			_ = file.Close()
			return nil, fmt.Errorf("timed out waiting for %s lock at %s after %s", kind, cleanPath, devLockTimeout)
		}
		time.Sleep(devLockRetryInterval)
	}
}

func checkDevLockOrder(path, kind string, order int) error {
	devHeldLocks.Lock()
	defer devHeldLocks.Unlock()
	if held, ok := devHeldLocks.byPath[path]; ok {
		return fmt.Errorf("lock ordering violation: %s lock at %s is already held by this process as %s", kind, path, held.kind)
	}
	for heldPath, held := range devHeldLocks.byPath {
		if held.order > order {
			return fmt.Errorf("lock ordering violation: refusing to acquire %s lock at %s while holding %s lock at %s", kind, path, held.kind, heldPath)
		}
	}
	return nil
}

func markDevLockHeld(path, kind string, order int) {
	devHeldLocks.Lock()
	defer devHeldLocks.Unlock()
	devHeldLocks.byPath[path] = heldDevLock{kind: kind, order: order}
}

func unmarkDevLockHeld(path string) {
	devHeldLocks.Lock()
	defer devHeldLocks.Unlock()
	delete(devHeldLocks.byPath, path)
}

func dbBranchRegistryLockPath(root string) string {
	return filepath.Join(root, "branches.lock")
}

func safeLockName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}
