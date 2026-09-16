//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package devprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func ConfigureChild(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func ConfigureDetachedChild(cmd *exec.Cmd) {
	ConfigureChild(cmd)
}

func InterruptTree(cmd *exec.Cmd) error {
	return signalProcessTree(cmd, syscall.SIGINT)
}

func KillTree(cmd *exec.Cmd) error {
	return signalProcessTree(cmd, syscall.SIGKILL)
}

// KillTreeConfirmed kills every process of the group a ConfigureChild command
// leads and waits, up to timeout, until none remains. The command's own exit
// does not end its descendants, such as the tools a build driver started.
func KillTreeConfirmed(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return nil
	}
	group := cmd.Process.Pid
	deadline := time.Now().Add(timeout)
	for {
		// A group whose remaining members are exited but not yet reaped answers
		// EPERM on some platforms (Darwin) instead of ESRCH; their new parent
		// reaps them shortly.
		err := syscall.Kill(-group, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still has members after %s: %v", group, timeout, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TerminateTreePID(pid int) error {
	return signalProcessIDTree(pid, syscall.SIGTERM)
}

func KillTreePID(pid int) error {
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

func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	ConfigureChild(cmd)
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
