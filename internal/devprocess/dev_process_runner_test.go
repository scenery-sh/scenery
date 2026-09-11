package devprocess

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestDevManagedProcessWaitReadySelectsDoneBeforeTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	done := make(chan struct{})
	close(done)
	process := testProcess(done)
	process.waitErr = errors.New("exit status 7")
	process.Tail.Add("ready-failed")
	start := time.Now()
	err := process.WaitReady(ctx, ReadyRequest{
		Timeout:  5 * time.Second,
		Interval: 25 * time.Millisecond,
		Probe: func(context.Context) error {
			return net.ErrClosed
		},
	})
	if err == nil {
		t.Fatal("WaitReady returned nil, want early exit error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("WaitReady took %s, want early exit before timeout", elapsed)
	}
	if got := err.Error(); !strings.Contains(got, "frontend web exited before becoming ready") || !strings.Contains(got, "ready-failed") {
		t.Fatalf("WaitReady error = %q, want early exit with output tail", got)
	}
}

func TestDevManagedProcessWaitReadySucceedsWhenProbePasses(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		process := testProcess(make(chan struct{}))
		calls := 0
		err := process.WaitReady(context.Background(), ReadyRequest{
			Timeout: time.Second, Interval: 25 * time.Millisecond,
			Probe: func(context.Context) error { calls++; return nil },
		})
		if err != nil || calls != 1 {
			t.Fatalf("WaitReady = %v, probe calls = %d; want success on the first probe", err, calls)
		}
	})
}

func TestDevManagedProcessWaitReadyProbeUsesFakeTicker(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		process := &ManagedProcess{
			Name:       "web",
			Kind:       "frontend",
			Tail:       &LineTail{limit: 10},
			Done:       make(chan struct{}),
			outputDone: make(chan struct{}),
		}
		var calls atomic.Int64
		errCh := make(chan error, 1)
		go func() {
			errCh <- process.WaitReady(context.Background(), ReadyRequest{
				Timeout:  5 * time.Second,
				Interval: 50 * time.Millisecond,
				Probe: func(context.Context) error {
					if calls.Add(1) < 3 {
						return os.ErrNotExist
					}
					return nil
				},
			})
		}()

		synctest.Wait()
		if got := calls.Load(); got != 0 {
			t.Fatalf("probe calls before first tick = %d, want 0", got)
		}
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("probe calls after first tick = %d, want 1", got)
		}
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("probe calls after second tick = %d, want 2", got)
		}
		time.Sleep(50 * time.Millisecond)
		if err := <-errCh; err != nil {
			t.Fatalf("WaitReady returned error: %v", err)
		}
		if got := calls.Load(); got != 3 {
			t.Fatalf("probe calls after success = %d, want 3", got)
		}
	})
}

func TestDevManagedProcessStopIsIdempotent(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	close(done)
	process := testProcess(done)
	if err := process.Stop(100 * time.Millisecond); err != nil {
		t.Fatalf("first Stop returned error: %v", err)
	}
	if err := process.Stop(100 * time.Millisecond); err != nil {
		t.Fatalf("second Stop returned error: %v", err)
	}
	reentered := false
	process.stopOnce.Do(func() { reentered = true })
	if reentered {
		t.Fatal("Stop did not consume its once guard")
	}
}

func TestDevManagedProcessStopRetainsUnconfirmedFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		process := testProcess(done)
		first := process.Stop(time.Millisecond)
		if first == nil {
			t.Fatal("Stop accepted an unconfirmed process exit")
		}
		if second := process.Stop(time.Millisecond); !errors.Is(second, first) {
			t.Fatalf("repeated Stop = %v, want retained failure %v", second, first)
		}
		close(done)
		if err := process.Stop(time.Millisecond); err != nil {
			t.Fatalf("confirmed later exit did not release stop failure: %v", err)
		}
	})
}

func TestDevManagedProcessWaitReadyTimeoutUsesFakeDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		process := &ManagedProcess{
			Name:       "web",
			Kind:       "frontend",
			Tail:       &LineTail{limit: 10},
			Done:       make(chan struct{}),
			outputDone: make(chan struct{}),
		}
		process.Tail.Add("still-starting")
		errCh := make(chan error, 1)
		go func() {
			errCh <- process.WaitReady(context.Background(), ReadyRequest{
				Timeout:  100 * time.Millisecond,
				Interval: 25 * time.Millisecond,
				Probe: func(context.Context) error {
					return os.ErrNotExist
				},
			})
		}()

		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		err := <-errCh
		if err == nil {
			t.Fatal("WaitReady returned nil, want timeout")
		}
		if got := err.Error(); !strings.Contains(got, "file does not exist") || !strings.Contains(got, "still-starting") {
			t.Fatalf("WaitReady error = %q, want last probe and output tail", got)
		}
	})
}

func testProcess(done <-chan struct{}) *ManagedProcess {
	outputDone := make(chan struct{})
	close(outputDone)
	return &ManagedProcess{Name: "web", Kind: "frontend", Tail: NewLineTail(10), Done: done, outputDone: outputDone}
}
