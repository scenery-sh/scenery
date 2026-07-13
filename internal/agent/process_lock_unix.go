//go:build unix

package agent

import (
	"os"
	"os/exec"
	"syscall"
)

func tryProcessLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockProcessLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func processLockBusy(err error) bool {
	return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN
}

func inheritProcessLock(file *os.File, cmd *exec.Cmd) (bool, error) {
	cmd.ExtraFiles = append(cmd.ExtraFiles, file)
	return true, nil
}
