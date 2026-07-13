//go:build windows

package agent

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func tryProcessLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func unlockProcessLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func processLockBusy(err error) bool {
	errno, ok := err.(windows.Errno)
	return ok && (errno == windows.ERROR_LOCK_VIOLATION || errno == windows.ERROR_SHARING_VIOLATION)
}

func inheritProcessLock(_ *os.File, _ *exec.Cmd) (bool, error) {
	return false, nil
}
