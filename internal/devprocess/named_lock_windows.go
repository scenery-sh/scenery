//go:build windows

package devprocess

import (
	"os"

	"golang.org/x/sys/windows"
)

func tryLockDevFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func unlockDevFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func isDevFileLockBusy(err error) bool {
	errno, ok := err.(windows.Errno)
	return ok && (errno == windows.ERROR_LOCK_VIOLATION || errno == windows.ERROR_SHARING_VIOLATION)
}
