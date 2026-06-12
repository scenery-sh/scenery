//go:build !unix

package main

import (
	"context"
	"os"
)

func getConsoleSize(file *os.File) terminalSize {
	return terminalSize{}
}

func notifyConsoleResize(ctx context.Context, stdin *os.File) <-chan terminalSize {
	return make(chan terminalSize)
}
