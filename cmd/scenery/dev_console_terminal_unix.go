//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func getConsoleSize(file *os.File) terminalSize {
	if file == nil {
		return terminalSize{}
	}
	ws, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws == nil {
		return terminalSize{}
	}
	return terminalSize{Width: int(ws.Col), Height: int(ws.Row)}
}

func notifyConsoleResize(ctx context.Context) <-chan terminalSize {
	out := make(chan terminalSize, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(sigCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				select {
				case out <- getConsoleSize(os.Stdin):
				default:
				}
			}
		}
	}()
	return out
}
