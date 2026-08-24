//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package workspacetx

import (
	"errors"
	"syscall"
)

func probeProcessLiveness(pid int) ownerProcessLiveness {
	if pid <= 0 {
		return ownerProcessUnknown
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return ownerProcessLive
	case errors.Is(err, syscall.ESRCH):
		return ownerProcessDead
	default:
		return ownerProcessUnknown
	}
}
