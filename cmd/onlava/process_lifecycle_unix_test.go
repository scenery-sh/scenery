//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorClosePropagatesCtrlCToAppProcessGroup(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	interruptPath := filepath.Join(dir, "interrupted")
	grandchildPath := filepath.Join(dir, "grandchild")

	cmd, done := startShellProcessTree(t, `
set -eu
trap 'echo interrupted > "$ONLAVA_TEST_INTERRUPTED"; kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 0' INT TERM
sleep 30 &
child=$!
echo "$child" > "$ONLAVA_TEST_GRANDCHILD"
echo ready > "$ONLAVA_TEST_READY"
wait "$child"
`, map[string]string{
		"ONLAVA_TEST_READY":       readyPath,
		"ONLAVA_TEST_INTERRUPTED": interruptPath,
		"ONLAVA_TEST_GRANDCHILD":  grandchildPath,
	})
	waitForTestFile(t, readyPath, time.Second)
	grandchildPID := readPIDFile(t, grandchildPath)

	supervisor := &devSupervisor{
		current: &runningApp{cmd: cmd, done: done},
	}
	if err := supervisor.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	waitForTestFile(t, interruptPath, time.Second)
	waitForProcessExit(t, cmd.Process.Pid, time.Second)
	waitForProcessExit(t, grandchildPID, time.Second)
}

func TestRunningAppWaitOrKillKillsStuckProcessGroup(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	grandchildPath := filepath.Join(dir, "grandchild")

	cmd, done := startShellProcessTree(t, `
set -eu
trap '' INT TERM
(trap '' INT TERM; sleep 30) &
child=$!
echo "$child" > "$ONLAVA_TEST_GRANDCHILD"
echo ready > "$ONLAVA_TEST_READY"
wait "$child"
`, map[string]string{
		"ONLAVA_TEST_READY":      readyPath,
		"ONLAVA_TEST_GRANDCHILD": grandchildPath,
	})
	waitForTestFile(t, readyPath, time.Second)
	grandchildPID := readPIDFile(t, grandchildPath)

	app := &runningApp{cmd: cmd, done: done}
	if err := app.interrupt(); err != nil {
		t.Fatalf("interrupt() error = %v", err)
	}
	start := time.Now()
	if err := app.waitOrKill(100 * time.Millisecond); err != nil {
		t.Fatalf("waitOrKill() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waitOrKill took %s, expected bounded SIGKILL path", elapsed)
	}
	waitForProcessExit(t, cmd.Process.Pid, time.Second)
	waitForProcessExit(t, grandchildPID, time.Second)
}

func TestSecondCtrlCUsesDefaultSignalBehavior(t *testing.T) {
	if os.Getenv("ONLAVA_TEST_SECOND_CTRL_C_HELPER") == "1" {
		runSecondCtrlCHelper()
		return
	}

	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	firstPath := filepath.Join(dir, "first")

	cmd := exec.Command(os.Args[0], "-test.run=TestSecondCtrlCUsesDefaultSignalBehavior")
	cmd.Env = append(os.Environ(),
		"ONLAVA_TEST_SECOND_CTRL_C_HELPER=1",
		"ONLAVA_TEST_READY="+readyPath,
		"ONLAVA_TEST_FIRST="+firstPath,
	)
	cmd.Stdin = nil
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer cleanupProcessTree(cmd, done)

	waitForTestFile(t, readyPath, time.Second)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("first interrupt: %v", err)
	}
	waitForTestFile(t, firstPath, time.Second)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("second interrupt: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("helper exited successfully; expected default SIGINT termination")
		}
		if !isSignalExit(err, syscall.SIGINT) {
			t.Fatalf("helper exit = %v, want SIGINT", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("helper did not exit after second Ctrl+C")
	}
}

func runSecondCtrlCHelper() {
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer stopSignals()

	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_READY"), []byte("ready"), 0o644)
	go func() {
		<-sigCtx.Done()
		stopSignals()
		cancel()
	}()

	<-ctx.Done()
	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_FIRST"), []byte("first"), 0o644)
	select {}
}

func startShellProcessTree(t *testing.T, script string, env map[string]string) (*exec.Cmd, chan error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = nil
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start shell process tree: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cleanupProcessTree(cmd, done)
	})
	return cmd, done
}

func cleanupProcessTree(cmd *exec.Cmd, done <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = killProcessTree(cmd)
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func waitForTestFile(t *testing.T, path string, timeout time.Duration) {
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

func readPIDFile(t *testing.T, path string) int {
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

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAliveForTest(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}

func processAliveForTest(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func isSignalExit(err error, sig syscall.Signal) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == sig
}
