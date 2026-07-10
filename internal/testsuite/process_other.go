//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package testsuite

import (
	"os/exec"
	"time"
)

func configureCommandCancellation(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.WaitDelay = 3 * time.Second
	}
}
