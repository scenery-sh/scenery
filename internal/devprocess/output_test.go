package devprocess

import (
	"regexp"
	"testing"
)

// TestStripANSIDoesNotAliasInput pins the invariant that lets stripANSI return
// ReplaceAll's buffer directly: the result must never share memory with the
// caller's slice, because callers retain it while the read buffer is reused.
func TestStripANSIDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"plain line",
		"\x1b[31mred\x1b[0m",
		"prefix \x1b[1mbold\x1b[0m suffix",
		"",
	} {
		data := []byte(input)
		got := stripANSI(data)
		if len(data) == 0 {
			if got != nil {
				t.Fatalf("stripANSI(%q) = %q, want nil for empty input", input, got)
			}
			continue
		}
		want := string(regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`).ReplaceAll(data, nil))
		if string(got) != want {
			t.Fatalf("stripANSI(%q) = %q, want %q", input, got, want)
		}
		// Mutating the caller's buffer must not change the returned value.
		before := string(got)
		for i := range data {
			data[i] = 'X'
		}
		if string(got) != before {
			t.Fatalf("stripANSI result aliases its input: %q became %q", before, got)
		}
	}
}
