//go:build windows

package agent

import (
	"os"
	"os/exec"
)

func configureAgentProcess(cmd *exec.Cmd) {
}

func interruptProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(os.Interrupt)
}

func terminateProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
