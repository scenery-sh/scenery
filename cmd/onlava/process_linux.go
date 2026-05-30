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

func configureDetachedChildProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptProcessTree(cmd *exec.Cmd) error {
	return signalProcessTree(cmd, syscall.SIGINT)
}

func killProcessTree(cmd *exec.Cmd) error {
	return signalProcessTree(cmd, syscall.SIGKILL)
}

func terminateProcessIDTree(pid int) error {
	return signalProcessIDTree(pid, syscall.SIGTERM)
}

func killProcessIDTree(pid int) error {
	return signalProcessIDTree(pid, syscall.SIGKILL)
}

func signalProcessTree(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	pid := cmd.Process.Pid
	return signalProcessIDTree(pid, sig)
}

func signalProcessIDTree(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 1 {
		if err := syscall.Kill(-pgid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		} else {
			return err
		}
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
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
