package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"scenery.sh/internal/devprocess"
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
	defer devprocess.StopDetached(cmd, reaped)
	startup := make(chan error, 1)
	go func() { _, err := io.ReadAll(reader); startup <- err }()
	waitCtx, stop := context.WithTimeout(parent, budget)
	defer stop()
	ctx, cancel := context.WithCancelCause(waitCtx)
	defer cancel(nil)
	monitorDone := make(chan struct{})
	go func() {
		devprocess.MonitorStartup(ctx, cancel, startup, exited, func(err error) error { return &harnessStartupExit{err} })
		close(monitorDone)
	}()
	<-ctx.Done()
	<-monitorDone
	failure := context.Cause(ctx)
	if reason == "timeout" {
		if !errors.Is(failure, context.DeadlineExceeded) {
			return fmt.Errorf("real detached timeout: %v", failure)
		}
	} else {
		var observed *harnessStartupExit
		var exit *exec.ExitError
		if !errors.As(failure, &observed) || !errors.As(failure, &exit) || exit.ExitCode() != 7 {
			return fmt.Errorf("real detached child exit: %v", failure)
		}
	}
	return nil
}

type harnessStartupExit struct{ err error }

func (e *harnessStartupExit) Error() string { return fmt.Sprintf("detached child exited: %v", e.err) }
func (e *harnessStartupExit) Unwrap() error { return e.err }
