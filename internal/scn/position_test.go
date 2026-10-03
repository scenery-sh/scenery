package scn

import (
	"bytes"
	"strings"
	"testing"
)

func TestPositionIndexMatchesScalarOffsets(t *testing.T) {
	for _, text := range []string{"", "\r\n\n", "one\r\ntwo\n", strings.Repeat("ascii ", 100), "Čeština🙂e\u0301\r\n終わり\n", "bad\xff\xe2\x80\nutf8"} {
		source := []byte(text)
		index := NewPositionIndex(source)
		for offset := -2; offset <= len(source)+2; offset++ {
			clamped := min(max(offset, 0), len(source))
			prefix := source[:clamped]
			start := bytes.LastIndexByte(prefix, '\n') + 1
			column := 0
			for runeOffset := range string(source[start:]) {
				if start+runeOffset >= clamped {
					break
				}
				column++
			}
			want := Position{Line: bytes.Count(prefix, []byte{'\n'}), Column: column, ByteOffset: clamped}
			if got := index.Position(offset); got != want {
				t.Fatalf("position(%q, %d)=%#v, want %#v", text, offset, got, want)
			}
		}
	}
}
