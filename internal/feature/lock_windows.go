//go:build windows

package feature

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
)

func lockFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}
func lockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
func protectCommand(_ *exec.Cmd, lease *os.File) error {
	if lease != nil {
		return errors.New("expensive feature checks require Unix child-owned probe admission")
	}
	return nil
}
