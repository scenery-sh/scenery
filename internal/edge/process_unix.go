//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package edge

import (
	"os/exec"
	"syscall"
)

func configureChildProcess(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func configureDetachedChildProcess(cmd *exec.Cmd) {
	configureChildProcess(cmd)
}
