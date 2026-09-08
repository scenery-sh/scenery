package devprocess

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestDevNamedLockBusyTimeoutHasNamedDiagnosticsInProcess(t *testing.T) {
	root := t.TempDir()
	var warnings strings.Builder
	options := shortLockOptions(&warnings)
	busy := errors.New("busy")
	now := time.Date(2026, 8, 25, 10, 30, 0, 0, time.UTC)
	_, err := acquireDevNamedLockWithDependencies(root, "substrate-postgres.lock", "shared substrate postgres", devLockOrderSubstrate, options, devNamedLockDependencies{
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
	root := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		var held atomic.Bool
		busy := errors.New("busy")
		deps := devNamedLockDependencies{
			now: time.Now, sleep: time.Sleep,
			tryLock: func(*os.File) error {
				if held.CompareAndSwap(false, true) {
					return nil
				}
				return busy
			},
			unlock: func(*os.File) error { held.Store(false); return nil },
			isBusy: func(err error) bool { return errors.Is(err, busy) },
		}
		acquire := func() (func(), error) {
			return acquireDevNamedLockWithDependencies(root, "substrate-postgres.lock", "shared substrate postgres", devLockOrderSubstrate, shortLockOptions(io.Discard), deps)
		}
		unlockFirst, err := acquire()
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			unlock func()
			err    error
		}
		acquired := make(chan result, 1)
		go func() {
			unlock, err := acquire()
			acquired <- result{unlock: unlock, err: err}
		}()
		synctest.Wait()
		select {
		case got := <-acquired:
			if got.unlock != nil {
				got.unlock()
			}
			t.Fatalf("second acquisition completed before first release: %v", got.err)
		default:
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
	})
}

func shortLockOptions(writer io.Writer) LockOptions {
	return LockOptions{RetryInterval: 10 * time.Millisecond, WarnAfter: 20 * time.Millisecond, Timeout: 120 * time.Millisecond, Warnings: writer}
}
