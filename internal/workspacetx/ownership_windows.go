//go:build windows

package workspacetx

import (
	"errors"
	"syscall"
)

const windowsErrorInvalidParameter syscall.Errno = 87

func processOwnerInfo(pid int) ownerProcessInfo {
	var info ownerProcessInfo
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return info
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// OpenProcess reports an absent positive PID as ERROR_INVALID_PARAMETER.
		if errors.Is(err, windowsErrorInvalidParameter) {
			info.Liveness = ownerProcessDead
		}
		return info
	}
	defer syscall.CloseHandle(handle)
	event, err := syscall.WaitForSingleObject(handle, 0)
	if err != nil {
		return info
	}
	switch event {
	case syscall.WAIT_TIMEOUT:
		info.Liveness = ownerProcessLive
	case syscall.WAIT_OBJECT_0:
		info.Liveness = ownerProcessDead
	}
	return info
}
