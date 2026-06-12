//go:build !unix

package main

import (
	"context"
	"os"
)

func getConsoleSize(file *os.File) terminalSize {
	return terminalSize{}
}

func notifyConsoleResize(ctx context.Context) <-chan terminalSize {
	return make(chan terminalSize)
}
