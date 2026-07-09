//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package edge

import "os/exec"

func configureChildProcess(cmd *exec.Cmd) {}

func configureDetachedChildProcess(cmd *exec.Cmd) {}
