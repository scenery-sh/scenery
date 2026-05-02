//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package dbstudio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandTreeContextCancelsDBStudioProcessTree(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	grandchildPath := filepath.Join(dir, "grandchild")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := commandTreeContext(ctx, "sh", "-c", `
set -eu
trap 'kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 0' INT TERM
sleep 30 &
child=$!
echo "$child" > "$ONLAVA_TEST_GRANDCHILD"
echo ready > "$ONLAVA_TEST_READY"
wait "$child"
`)
	cmd.Env = append(os.Environ(),
		"ONLAVA_TEST_READY="+readyPath,
		"ONLAVA_TEST_GRANDCHILD="+grandchildPath,
	)
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbstudio command tree: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = killProcessTree(cmd)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()

	waitForDBStudioTestFile(t, readyPath, time.Second)
	grandchildPID := readDBStudioPIDFile(t, grandchildPath)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !isExpectedExit(err) {
			t.Fatalf("dbstudio command tree exit = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dbstudio command tree did not exit after context cancel")
	}
	waitForDBStudioProcessExit(t, grandchildPID, time.Second)
}

func waitForDBStudioTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func readDBStudioPIDFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", data, err)
	}
	return pid
}

func waitForDBStudioProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !dbStudioProcessAliveForTest(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}

func dbStudioProcessAliveForTest(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
