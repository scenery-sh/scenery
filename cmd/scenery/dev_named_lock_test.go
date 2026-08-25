package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDevNamedLockBusyTimeoutHasNamedDiagnosticsInProcess(t *testing.T) {
	root := t.TempDir()
	var warnings strings.Builder
	restore := setDevLockTestTiming(&warnings)
	defer restore()
	busy := errors.New("busy")
	now := time.Date(2026, 8, 25, 10, 30, 0, 0, time.UTC)
	_, err := acquireDevNamedLockWithDependencies(root, "substrate-postgres.lock", "shared substrate postgres", devLockOrderSubstrate, devNamedLockDependencies{
		now: func() time.Time { return now },
		sleep: func(duration time.Duration) {
			now = now.Add(duration)
		},
		tryLock: func(*os.File) error { return busy },
		isBusy:  func(err error) bool { return errors.Is(err, busy) },
	})
	if err == nil {
		t.Fatal("busy lock unexpectedly succeeded")
	}
	got := warnings.String() + err.Error()
	for _, want := range []string{
		"waiting for shared substrate postgres lock at",
		"timed out waiting for shared substrate postgres lock",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subprocess output missing %q:\n%s", want, got)
		}
	}
}

func TestDevNamedLockSerializesSameProcessAcquisition(t *testing.T) {
	restore := setDevLockTestTiming(io.Discard)
	defer restore()
	root := t.TempDir()
	unlockFirst, err := lockManagedSubstrateRoot(root, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		unlock func()
		err    error
	}
	acquired := make(chan result, 1)
	go func() {
		unlock, err := lockManagedSubstrateRoot(root, "postgres")
		acquired <- result{unlock: unlock, err: err}
	}()
	select {
	case got := <-acquired:
		if got.unlock != nil {
			got.unlock()
		}
		t.Fatalf("second acquisition completed before first release: %v", got.err)
	case <-time.After(30 * time.Millisecond):
	}
	unlockFirst()
	select {
	case got := <-acquired:
		if got.err != nil {
			t.Fatal(got.err)
		}
		got.unlock()
	case <-time.After(time.Second):
		t.Fatal("second acquisition did not complete after first release")
	}
}

func setDevLockTestTiming(writer io.Writer) func() {
	oldRetry := devLockRetryInterval
	oldWarn := devLockWarnAfter
	oldTimeout := devLockTimeout
	oldWriter := devLockWarnWriter
	devLockRetryInterval = 10 * time.Millisecond
	devLockWarnAfter = 20 * time.Millisecond
	devLockTimeout = 120 * time.Millisecond
	devLockWarnWriter = writer
	return func() {
		devLockRetryInterval = oldRetry
		devLockWarnAfter = oldWarn
		devLockTimeout = oldTimeout
		devLockWarnWriter = oldWriter
	}
}
