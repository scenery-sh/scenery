//go:build darwin

package main

import "golang.org/x/sys/unix"

const termiosGet, termiosSet = uint(unix.TIOCGETA), uint(unix.TIOCSETA)
