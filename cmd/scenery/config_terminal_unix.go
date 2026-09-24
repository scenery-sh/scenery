//go:build darwin || linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readHiddenLine prompts on the terminal and reads one line with echo off.
// The submission newline is not part of the value.
func readHiddenLine(terminal *os.File, prompt io.Writer, label string) ([]byte, error) {
	fd := int(terminal.Fd())
	get, set := termiosGet, termiosSet
	state, err := unix.IoctlGetTermios(fd, get)
	if err != nil {
		return nil, fmt.Errorf("read hidden input: %w", err)
	}
	hidden := *state
	hidden.Lflag &^= unix.ECHO
	hidden.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, set, &hidden); err != nil {
		return nil, fmt.Errorf("read hidden input: %w", err)
	}
	defer func() { _ = unix.IoctlSetTermios(fd, set, state) }()
	_, _ = fmt.Fprintf(prompt, "%s: ", label)
	line, err := bufio.NewReader(io.LimitReader(terminal, int64(maxConfigInputBytes)+2)).ReadBytes('\n')
	_, _ = fmt.Fprintln(prompt)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
	}
	return line, nil
}
