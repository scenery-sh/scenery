//go:build linux

package main

import "golang.org/x/sys/unix"

const termiosGet, termiosSet = uint(unix.TCGETS), uint(unix.TCSETS)
