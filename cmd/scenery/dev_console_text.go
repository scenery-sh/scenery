package main

import (
	"strings"
	"unicode/utf8"
)

func fitStyledLine(line string, width int) string {
	width = normalizeTerminalSize(terminalSize{Width: width, Height: defaultConsoleHeight}).Width
	return truncateANSI(line, width)
}

func padStyledLine(line string, width int) string {
	line = fitStyledLine(line, width)
	visible := visibleStringWidth(line)
	if visible >= width {
		return line
	}
	return line + strings.Repeat(" ", width-visible)
}

func truncateANSI(line string, width int) string {
	if width <= 0 || visibleStringWidth(line) <= width {
		return line
	}
	if width == 1 {
		return "."
	}
	var b strings.Builder
	visible := 0
	limit := width - 1
	suffix := "."
	if width >= 4 {
		limit = width - 3
		suffix = "..."
	}
	for i := 0; i < len(line) && visible < limit; {
		if line[i] == '\x1b' {
			next := i + 1
			for next < len(line) {
				ch := line[next]
				next++
				if ch >= '@' && ch <= '~' {
					break
				}
			}
			b.WriteString(line[i:next])
			i = next
			continue
		}
		r, size := rune(line[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(line[i:])
			if r == utf8.RuneError && size == 0 {
				break
			}
		}
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		b.WriteRune(r)
		visible++
		i += size
	}
	b.WriteString(suffix)
	if strings.Contains(line, "\x1b[") {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func visibleStringWidth(line string) int {
	width := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			i++
			for i < len(line) {
				ch := line[i]
				i++
				if ch >= '@' && ch <= '~' {
					break
				}
			}
			continue
		}
		r, size := rune(line[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(line[i:])
			if r == utf8.RuneError && size == 0 {
				break
			}
		}
		if r != '\n' && r != '\r' {
			width++
		}
		i += size
	}
	return width
}
