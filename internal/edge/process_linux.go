//go:build linux

package edge

import (
	"os/exec"
	"syscall"
)

func configureChildProcess(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	}
}

func configureDetachedChildProcess(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}
