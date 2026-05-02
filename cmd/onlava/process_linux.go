//go:build linux

package main

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureChildProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGTERM,
	}
}

func interruptProcessTree(cmd *exec.Cmd) error {
	return signalProcessTree(cmd, syscall.SIGINT)
}

func killProcessTree(cmd *exec.Cmd) error {
	return signalProcessTree(cmd, syscall.SIGKILL)
}

func signalProcessTree(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	pid := cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 1 {
		if err := syscall.Kill(-pgid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		} else {
			return err
		}
	}
	if err := cmd.Process.Signal(sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func commandTreeContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureChildProcess(cmd)
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
