//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package devprocess

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func ConfigureChild(cmd *exec.Cmd) {}

func ConfigureDetachedChild(cmd *exec.Cmd) {}

func InterruptTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

func KillTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGKILL)
}

func TerminateTreePID(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(os.Interrupt)
}

func KillTreePID(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGKILL)
}

func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	ConfigureCancellation(cmd, 3*time.Second)
	return cmd
}

func ConfigureCancellation(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil {
		return
	}
	cmd.WaitDelay = grace + time.Second
	cmd.Cancel = func() error {
		if err := InterruptTree(cmd); err != nil {
			return err
		}
		if grace <= 0 {
			return nil
		}
		go func() {
			timer := time.NewTimer(grace)
			defer timer.Stop()
			<-timer.C
			_ = KillTree(cmd)
		}()
		return nil
	}
}
