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

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
)

func TestSupervisorClosePropagatesCtrlCToAppProcessGroup(t *testing.T) {
	if os.Getenv("ONLAVA_TEST_SUPERVISOR_CLOSE_HELPER") == "1" {
		runSupervisorCloseHelper()
		return
	}
	t.Parallel()

	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	interruptPath := filepath.Join(dir, "interrupted")
	grandchildPath := filepath.Join(dir, "grandchild")

	cmd := exec.Command(os.Args[0], "-test.run=TestSupervisorClosePropagatesCtrlCToAppProcessGroup")
	cmd.Env = append(os.Environ(),
		"ONLAVA_TEST_SUPERVISOR_CLOSE_HELPER=1",
		"ONLAVA_TEST_READY="+readyPath,
		"ONLAVA_TEST_INTERRUPTED="+interruptPath,
		"ONLAVA_TEST_GRANDCHILD="+grandchildPath,
	)
	cmd.Stdin = nil
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start supervisor close helper: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cleanupProcessTree(cmd, done)
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
	t.Parallel()

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

func TestSupervisorCloseStopsTypeScriptWorkerAndClearsState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	interruptPath := filepath.Join(dir, "interrupted")

	cmd, done := startShellProcessTree(t, `
set -eu
trap 'echo interrupted > "$ONLAVA_TEST_INTERRUPTED"; exit 0' INT TERM
echo ready > "$ONLAVA_TEST_READY"
while true; do sleep 1; done
`, map[string]string{
		"ONLAVA_TEST_READY":       readyPath,
		"ONLAVA_TEST_INTERRUPTED": interruptPath,
	})
	waitForTestFile(t, readyPath, time.Second)

	supervisor := &devSupervisor{
		typescript: &runningTypeScriptWorker{
			cmd:  cmd,
			done: done,
			pid:  strconv.Itoa(cmd.Process.Pid),
		},
	}
	if err := supervisor.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	waitForTestFile(t, interruptPath, time.Second)
	waitForProcessExit(t, cmd.Process.Pid, time.Second)
	if got := supervisor.currentTypeScriptWorker(); got != nil {
		t.Fatalf("currentTypeScriptWorker() = %#v, want nil", got)
	}
}

func TestDetachedDevReapsStaleTypeScriptWorker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outputDir := filepath.Join(root, ".onlava", "generated", "temporal", "typescript")
	workerPath := filepath.Join(outputDir, "worker.ts")
	readyPath := filepath.Join(root, "ready")
	interruptPath := filepath.Join(root, "interrupted")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workerPath, []byte(`#!/bin/sh
set -eu
trap 'echo interrupted > "$ONLAVA_TEST_INTERRUPTED"; exit 0' INT TERM
echo ready > "$ONLAVA_TEST_READY"
while true; do sleep 1; done
`), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", workerPath)
	cmd.Env = append(os.Environ(),
		"ONLAVA_TEST_READY="+readyPath,
		"ONLAVA_TEST_INTERRUPTED="+interruptPath,
	)
	cmd.Stdin = nil
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stale worker fixture: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cleanupProcessTree(cmd, done)
	})
	waitForTestFile(t, readyPath, time.Second)

	record := typeScriptWorkerDevRegistry{
		SchemaVersion: "onlava.dev.typescript_worker.v1",
		PID:           cmd.Process.Pid,
		AppRoot:       cleanAbsPath(root),
		OutputDir:     cleanAbsPath(outputDir),
		WorkerPath:    cleanAbsPath(workerPath),
		Command:       []string{"sh", workerPath},
		DevSupervisor: true,
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeTypeScriptWorkerDevRegistry(outputDir, record); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer store.Close()
	supervisor := &devSupervisor{
		root:  root,
		cfg:   app.Config{Name: "demo"},
		store: store,
	}

	if err := supervisor.reapStaleTypeScriptWorker(ctx, outputDir); err != nil {
		t.Fatalf("reap outside detached mode: %v", err)
	}
	if !processAliveForTest(cmd.Process.Pid) {
		t.Fatal("stale worker was reaped outside detached mode")
	}

	t.Setenv(detachedDevChildEnv, "1")
	if err := supervisor.reapStaleTypeScriptWorker(ctx, outputDir); err != nil {
		t.Fatalf("reap detached worker: %v", err)
	}
	waitForTestFile(t, interruptPath, time.Second)
	waitForProcessExit(t, cmd.Process.Pid, time.Second)
	if _, ok, err := readTypeScriptWorkerDevRegistry(outputDir); err != nil || ok {
		t.Fatalf("registry after reap ok=%v err=%v", ok, err)
	}
	events, err := store.ListProcessEvents(ctx, "demo", 10)
	if err != nil {
		t.Fatalf("ListProcessEvents() error = %v", err)
	}
	for _, event := range events {
		if event.Kind == "typescript-worker-stale-reap" {
			return
		}
	}
	t.Fatalf("missing stale reap event: %#v", events)
}

func TestSecondCtrlCUsesDefaultSignalBehavior(t *testing.T) {
	if os.Getenv("ONLAVA_TEST_SECOND_CTRL_C_HELPER") == "1" {
		runSecondCtrlCHelper()
		return
	}
	t.Parallel()

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
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
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

func runSupervisorCloseHelper() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	child := exec.Command("sleep", "30")
	child.Stdin = nil
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_GRANDCHILD"), []byte(strconv.Itoa(child.Process.Pid)), 0o644)
	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_READY"), []byte("ready"), 0o644)

	<-signals
	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_INTERRUPTED"), []byte("interrupted"), 0o644)
	if child.Process != nil {
		_ = child.Process.Kill()
	}
	_ = child.Wait()
	os.Exit(0)
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
