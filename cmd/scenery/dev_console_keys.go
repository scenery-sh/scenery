package main

import (
	"bufio"
	"io"
	"strings"
)

type consoleKeyKind int

const (
	consoleKeyRune consoleKeyKind = iota
	consoleKeyEnter
	consoleKeyEsc
	consoleKeyBackspace
	consoleKeyTab
	consoleKeyCtrlC
	consoleKeyCtrlL
	consoleKeyUp
	consoleKeyDown
	consoleKeyLeft
	consoleKeyRight
	consoleKeyPageUp
	consoleKeyPageDown
	consoleKeyHome
	consoleKeyEnd
	consoleKeyMouseWheelUp
	consoleKeyMouseWheelDown
)

type consoleKey struct {
	Kind consoleKeyKind
	Rune rune
}

func readConsoleKeys(stdin io.Reader, keys chan<- consoleKey) {
	defer close(keys)
	reader := bufio.NewReader(stdin)
	for {
		key, err := readConsoleKey(reader)
		if err != nil {
			return
		}
		keys <- key
	}
}

func readConsoleKey(reader *bufio.Reader) (consoleKey, error) {
	r, _, err := reader.ReadRune()
	if err != nil {
		return consoleKey{}, err
	}
	switch r {
	case 3:
		return consoleKey{Kind: consoleKeyCtrlC}, nil
	case 9:
		return consoleKey{Kind: consoleKeyTab}, nil
	case 12:
		return consoleKey{Kind: consoleKeyCtrlL}, nil
	case '\r', '\n':
		return consoleKey{Kind: consoleKeyEnter}, nil
	case 27:
		return readConsoleEscapeKey(reader), nil
	case 127, '\b':
		return consoleKey{Kind: consoleKeyBackspace}, nil
	default:
		return consoleKey{Kind: consoleKeyRune, Rune: r}, nil
	}
}

func readConsoleEscapeKey(reader *bufio.Reader) consoleKey {
	if reader.Buffered() == 0 {
		return consoleKey{Kind: consoleKeyEsc}
	}
	next, err := reader.Peek(1)
	if err != nil || len(next) == 0 {
		return consoleKey{Kind: consoleKeyEsc}
	}
	if next[0] != '[' && next[0] != 'O' {
		_, _ = reader.ReadByte()
		return consoleKey{Kind: consoleKeyEsc}
	}
	prefix, _ := reader.ReadByte()
	seq := []byte{prefix}
	for len(seq) < 32 {
		ch, err := reader.ReadByte()
		if err != nil {
			break
		}
		seq = append(seq, ch)
		if (ch >= 'A' && ch <= 'Z') || ch == '~' || ch == 'M' || ch == 'm' {
			break
		}
	}
	switch string(seq) {
	case "[A", "OA":
		return consoleKey{Kind: consoleKeyUp}
	case "[B", "OB":
		return consoleKey{Kind: consoleKeyDown}
	case "[C", "OC":
		return consoleKey{Kind: consoleKeyRight}
	case "[D", "OD":
		return consoleKey{Kind: consoleKeyLeft}
	case "[H", "OH", "[1~", "[7~":
		return consoleKey{Kind: consoleKeyHome}
	case "[F", "OF", "[4~", "[8~":
		return consoleKey{Kind: consoleKeyEnd}
	case "[5~":
		return consoleKey{Kind: consoleKeyPageUp}
	case "[6~":
		return consoleKey{Kind: consoleKeyPageDown}
	}
	text := string(seq)
	if strings.HasPrefix(text, "[<64;") {
		return consoleKey{Kind: consoleKeyMouseWheelUp}
	}
	if strings.HasPrefix(text, "[<65;") {
		return consoleKey{Kind: consoleKeyMouseWheelDown}
	}
	return consoleKey{Kind: consoleKeyEsc}
}
