package main

import (
	"fmt"
	"io"
	"strings"
)

type consoleDiffRenderer struct {
	out    io.Writer
	size   terminalSize
	lines  []string
	closed bool
}

func newConsoleDiffRenderer(out io.Writer, size terminalSize) *consoleDiffRenderer {
	return &consoleDiffRenderer{out: out, size: normalizeTerminalSize(size)}
}

func (r *consoleDiffRenderer) Resize(size terminalSize) {
	r.size = normalizeTerminalSize(size)
	if len(r.lines) > r.size.Height {
		r.lines = r.lines[:r.size.Height]
	}
}

func (r *consoleDiffRenderer) Render(frame string) error {
	if r.closed {
		return nil
	}
	next := strings.Split(frame, "\n")
	if len(next) > r.size.Height {
		next = next[:r.size.Height]
	}
	for len(next) < r.size.Height {
		next = append(next, "")
	}
	maxRows := maxInt(len(next), len(r.lines))
	if maxRows > r.size.Height {
		maxRows = r.size.Height
	}
	var b strings.Builder
	for i := 0; i < maxRows; i++ {
		prevLine := ""
		if i < len(r.lines) {
			prevLine = r.lines[i]
		}
		nextLine := ""
		if i < len(next) {
			nextLine = next[i]
		}
		if prevLine == nextLine {
			continue
		}
		fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[K", i+1, nextLine)
	}
	if b.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(r.out, b.String())
	r.lines = next
	return err
}
