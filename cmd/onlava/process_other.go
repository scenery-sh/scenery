//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureChildProcess(cmd *exec.Cmd) {}

func interruptProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGKILL)
}

func commandTreeContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommandCancellation(cmd, 3*time.Second)
	return cmd
}

func configureCommandCancellation(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil {
		return
	}
	cmd.WaitDelay = grace + time.Second
	cmd.Cancel = func() error {
		if err := interruptProcessTree(cmd); err != nil {
			return err
		}
		if grace <= 0 {
			return nil
		}
		go func() {
			timer := time.NewTimer(grace)
			defer timer.Stop()
			<-timer.C
			_ = killProcessTree(cmd)
		}()
		return nil
	}
}
