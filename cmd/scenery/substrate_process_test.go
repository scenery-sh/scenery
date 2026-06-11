package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenSubstrateLogWritersWritesSeparateFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	logs, err := openSubstrateLogWriters(root, "victoria", "traces", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logs.stdout.Write([]byte("stdout\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.stderr.Write([]byte("stderr\n")); err != nil {
		t.Fatal(err)
	}
	if err := logs.close(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(logs.stdoutPath, filepath.Join(".scenery", "substrates", "victoria", "logs", "victoria.traces.stdout.log")) {
		t.Fatalf("stdout path = %q", logs.stdoutPath)
	}
	if got, err := os.ReadFile(logs.stdoutPath); err != nil || string(got) != "stdout\n" {
		t.Fatalf("stdout log = %q err=%v", got, err)
	}
	if got, err := os.ReadFile(logs.stderrPath); err != nil || string(got) != "stderr\n" {
		t.Fatalf("stderr log = %q err=%v", got, err)
	}
}

func TestSubstrateExitRecordCapturesExitCodeAndLogPaths(t *testing.T) {
	t.Parallel()

	if os.Getenv("SCENERY_SUBSTRATE_EXIT_HELPER") == "1" {
		os.Exit(7)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSubstrateExitRecordCapturesExitCodeAndLogPaths")
	cmd.Env = append(os.Environ(), "SCENERY_SUBSTRATE_EXIT_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatal("helper exited successfully, want non-zero")
	}
	exit := substrateExitRecord("server", cmd.Process.Pid, time.Now().Add(-time.Second).UTC(), "/tmp/stdout.log", "/tmp/stderr.log", err, cmd.ProcessState)
	if exit.ExitCode != 7 || exit.Component != "server" || exit.PID != cmd.Process.Pid {
		t.Fatalf("exit record = %+v", exit)
	}
	if exit.LogPath != "/tmp/stderr.log" || exit.StdoutLogPath != "/tmp/stdout.log" || exit.StderrLogPath != "/tmp/stderr.log" {
		t.Fatalf("exit log paths = %+v", exit)
	}
}
