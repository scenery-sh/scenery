package devprocess

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const devLockOrderSubstrate = 10

// Substrate locks serialize slow shared substrate startup (for example the
// managed Postgres server ensure path). The process-local held-lock set below
// rejects lower-order inversions before the OS lock can block forever. The OS
// lock serializes concurrent acquisition of the same path within and across
// processes.
var (
	devHeldLocks = struct {
		sync.Mutex
		byPath map[string]heldDevLock
	}{byPath: map[string]heldDevLock{}}
)

type heldDevLock struct {
	kind  string
	order int
}

type devNamedLockDependencies struct {
	now     func() time.Time
	sleep   func(time.Duration)
	tryLock func(*os.File) error
	unlock  func(*os.File) error
	isBusy  func(error) bool
}

// LockOptions bounds substrate lock acquisition and its progress output.
// Zero durations preserve the existing production deadlines.
type LockOptions struct {
	RetryInterval time.Duration
	WarnAfter     time.Duration
	WarnRepeat    time.Duration
	Timeout       time.Duration
	Warnings      io.Writer
}

func (o LockOptions) defaults() LockOptions {
	if o.RetryInterval <= 0 {
		o.RetryInterval = 50 * time.Millisecond
	}
	if o.WarnAfter <= 0 {
		o.WarnAfter = 2 * time.Second
	}
	if o.WarnRepeat <= 0 {
		o.WarnRepeat = 15 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Warnings == nil {
		o.Warnings = os.Stderr
	}
	return o
}

func LockSubstrate(root, kind string, options LockOptions) (func(), error) {
	name := "substrate.lock"
	if kind = strings.TrimSpace(kind); kind != "" {
		name = "substrate-" + safeLockName(kind) + ".lock"
	}
	if kind == "" {
		kind = "unknown"
	}
	return acquireDevNamedLockWithDependencies(root, name, "shared substrate "+kind, devLockOrderSubstrate, options, devNamedLockDependencies{
		now: time.Now, sleep: time.Sleep, tryLock: tryLockDevFile, unlock: unlockDevFile, isBusy: isDevFileLockBusy,
	})
}

func acquireDevNamedLockWithDependencies(root, name, kind string, order int, options LockOptions, dependencies devNamedLockDependencies) (func(), error) {
	options = options.defaults()
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
	start := dependencies.now()
	nextWarning := options.WarnAfter
	for {
		err := dependencies.tryLock(file)
		if err == nil {
			markDevLockHeld(cleanPath, kind, order)
			return func() {
				unmarkDevLockHeld(cleanPath)
				_ = dependencies.unlock(file)
				_ = file.Close()
			}, nil
		}
		if !dependencies.isBusy(err) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s at %s: %w", kind, cleanPath, err)
		}
		elapsed := dependencies.now().Sub(start)
		if elapsed >= nextWarning {
			if options.Warnings != nil {
				_, _ = fmt.Fprintf(options.Warnings, "waiting for %s lock at %s (%s elapsed)\n", kind, cleanPath, elapsed.Round(time.Second))
			}
			nextWarning += options.WarnRepeat
		}
		if elapsed >= options.Timeout {
			_ = file.Close()
			return nil, fmt.Errorf("timed out waiting for %s lock at %s after %s", kind, cleanPath, options.Timeout)
		}
		dependencies.sleep(options.RetryInterval)
	}
}

func checkDevLockOrder(path, kind string, order int) error {
	devHeldLocks.Lock()
	defer devHeldLocks.Unlock()
	for heldPath, held := range devHeldLocks.byPath {
		if heldPath == path {
			continue
		}
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
