//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package victoria

import (
	"os"
	"syscall"
)

func processExitSignal(state *os.ProcessState) string {
	if state == nil {
		return ""
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}
