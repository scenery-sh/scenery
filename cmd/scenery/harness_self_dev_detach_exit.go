package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

func runHarnessDetachedExitProbe(parent context.Context) error {
	for _, reason := range []string{"child_exit", "timeout"} {
		if err := runHarnessDetachedExitCase(parent, reason); err != nil {
			return err
		}
	}
	return nil
}

func runHarnessDetachedExitCase(parent context.Context, reason string) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	script := "echo untrusted-child-output >&2; exit 7"
	budget := time.Second
	if reason == "timeout" {
		script = "exec 3>&-; echo untrusted-child-output; exec sleep 30"
		budget = 40 * time.Millisecond
	}
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	configureDetachedChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = writer.Close()
	exited := make(chan error, 1)
	reaped := make(chan struct{})
	go func() { exited <- cmd.Wait(); close(reaped) }()
	defer stopDetachedDevChild(cmd, reaped)
	startup := make(chan error, 1)
	go func() { startup <- readDetachedDevStartupResult(reader) }()
	waitCtx, stop := context.WithTimeout(parent, budget)
	defer stop()
	ctx, cancel := context.WithCancelCause(waitCtx)
	defer cancel(nil)
	monitorDone := make(chan struct{})
	go func() { monitorDetachedDevStartup(ctx, cancel, startup, exited); close(monitorDone) }()
	<-ctx.Done()
	<-monitorDone
	failure := detachedDevWaitFailure(context.Cause(ctx), cmd.Process.Pid, "ready", budget, "/private/startup.log")
	diagnostic := cliErrorDiagnostic(failure)
	if cliExitCode(failure) != 3 || diagnostic.Details["detached_startup"].(map[string]any)["reason"] != reason {
		return fmt.Errorf("real detached %s classification: %v", reason, failure)
	}
	return nil
}
