package main

import (
	"context"
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
	process, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name:    "web",
		Kind:    "frontend",
		Command: "/bin/sh",
		Args:    []string{"-c", "echo ready-failed; exit 7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = process.WaitReady(ctx, devProcessReadyRequest{
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	process, err := startDevManagedProcess(context.Background(), devProcessStartRequest{
		Name:    "web",
		Kind:    "frontend",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Stop(100 * time.Millisecond) }()

	err = process.WaitReady(context.Background(), devProcessReadyRequest{
		Timeout:  time.Second,
		Interval: 25 * time.Millisecond,
		Probe: func(context.Context) error {
			conn, err := net.DialTimeout("tcp", ln.Addr().String(), 50*time.Millisecond)
			if err != nil {
				return err
			}
			_ = conn.Close()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("WaitReady returned error: %v", err)
	}
}

func TestDevManagedProcessWaitReadyProbeUsesFakeTicker(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		process := &devManagedProcess{
			Name:       "web",
			Kind:       "frontend",
			Tail:       &safeLineTail{limit: 10},
			done:       make(chan struct{}),
			outputDone: make(chan struct{}),
		}
		var calls atomic.Int64
		errCh := make(chan error, 1)
		go func() {
			errCh <- process.WaitReady(context.Background(), devProcessReadyRequest{
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

	process, err := startDevManagedProcess(context.Background(), devProcessStartRequest{
		Name:    "web",
		Kind:    "frontend",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Stop(100 * time.Millisecond); err != nil {
		t.Fatalf("first Stop returned error: %v", err)
	}
	if err := process.Stop(100 * time.Millisecond); err != nil {
		t.Fatalf("second Stop returned error: %v", err)
	}
}

func TestDevManagedProcessWaitReadyTimeoutUsesFakeDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		process := &devManagedProcess{
			Name:       "web",
			Kind:       "frontend",
			Tail:       &safeLineTail{limit: 10},
			done:       make(chan struct{}),
			outputDone: make(chan struct{}),
		}
		process.Tail.Add("still-starting")
		errCh := make(chan error, 1)
		go func() {
			errCh <- process.WaitReady(context.Background(), devProcessReadyRequest{
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

func TestDevManagedProcessStartupTimeoutIncludesLastProbeAndTail(t *testing.T) {
	t.Parallel()

	process, err := startDevManagedProcess(context.Background(), devProcessStartRequest{
		Name:    "web",
		Kind:    "frontend",
		Command: "/bin/sh",
		Args:    []string{"-c", "echo still-starting; sleep 5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Stop(100 * time.Millisecond) }()

	err = process.WaitReady(context.Background(), devProcessReadyRequest{
		Timeout:  100 * time.Millisecond,
		Interval: 25 * time.Millisecond,
		Probe: func(context.Context) error {
			return os.ErrNotExist
		},
	})
	if err == nil {
		t.Fatal("WaitReady returned nil, want timeout")
	}
	if got := err.Error(); !strings.Contains(got, "file does not exist") || !strings.Contains(got, "still-starting") {
		t.Fatalf("WaitReady error = %q, want last probe and output tail", got)
	}
}
