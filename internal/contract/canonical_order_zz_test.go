package contract

import (
	"math/rand/v2"
	"regexp"
	"sort"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// referenceContractUTF16Less walks the UTF-16 sequence directly instead of
// materializing it. Contract wire bytes depend on object-key order, so this
// independent comparator must agree with contractUTF16Less on every input.
func referenceContractUTF16Less(left, right string) bool {
	a, b := referenceUTF16Iterator{value: left}, referenceUTF16Iterator{value: right}
	for {
		leftUnit, leftOK := a.next()
		rightUnit, rightOK := b.next()
		if !leftOK || !rightOK {
			return !leftOK && rightOK
		}
		if leftUnit != rightUnit {
			return leftUnit < rightUnit
		}
	}
}

type referenceUTF16Iterator struct {
	value       string
	byteIndex   int
	trailing    uint16
	hasTrailing bool
}

func (i *referenceUTF16Iterator) next() (uint16, bool) {
	if i.hasTrailing {
		i.hasTrailing = false
		return i.trailing, true
	}
	if i.byteIndex == len(i.value) {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(i.value[i.byteIndex:])
	i.byteIndex += size
	if r <= 0xffff {
		return uint16(r), true
	}
	high, low := utf16.EncodeRune(r)
	i.trailing = uint16(low)
	i.hasTrailing = true
	return uint16(high), true
}

func TestContractUTF16LessMatchesReference(t *testing.T) {
	t.Parallel()

	words := []string{
		"", "a", "b", "A", "Z", "_", "-", ".", "0", "9", "~", "\x7f",
		"aa", "ab", "a_", "az", "aZ",
		"é", "ü", "ß", "中", "日本",
		"\uE000", "\uF8FF", "\uFFFD", "\uFFFF",
		"\U00010000", "\U0001F600", "\U0010FFFF",
		"a\uE000", "a\U0001F600", "\U0001F600a",
		"id", "name", "value", "Value", "value2",
	}
	for _, left := range words {
		for _, right := range words {
			if got, want := contractUTF16Less(left, right), referenceContractUTF16Less(left, right); got != want {
				t.Fatalf("contractUTF16Less(%q, %q) = %v, reference = %v", left, right, got, want)
			}
		}
	}

	source := rand.New(rand.NewPCG(23, 29))
	alphabet := []rune{'a', 'b', 'Z', '_', '0', 'é', '中', '\uE000', '\uFFFF', '\U0001F600', '\U0010FFFF'}
	word := func() string {
		var runes [5]rune
		length := source.IntN(len(runes) + 1)
		for i := range length {
			runes[i] = alphabet[source.IntN(len(alphabet))]
		}
		return string(runes[:length])
	}
	// Reuse a broad deterministic corpus so the 300,000 differential checks
	// exercise independently selected pairs without allocating two throwaway
	// strings for every comparison.
	randomWords := make([]string, 1<<16)
	for i := range randomWords {
		randomWords[i] = word()
	}
	for range 300000 {
		left := randomWords[source.IntN(len(randomWords))]
		right := randomWords[source.IntN(len(randomWords))]
		if got, want := contractUTF16Less(left, right), referenceContractUTF16Less(left, right); got != want {
			t.Fatalf("contractUTF16Less(%q, %q) = %v, reference = %v", left, right, got, want)
		}
	}

	for range 3000 {
		keys := make([]string, source.IntN(12))
		for i := range keys {
			keys[i] = word()
		}
		fast := append([]string(nil), keys...)
		slow := append([]string(nil), keys...)
		sort.SliceStable(fast, func(i, j int) bool { return contractUTF16Less(fast[i], fast[j]) })
		sort.SliceStable(slow, func(i, j int) bool { return referenceContractUTF16Less(slow[i], slow[j]) })
		for i := range fast {
			if fast[i] != slow[i] {
				t.Fatalf("sort order diverged at %d: %q vs %q", i, fast, slow)
			}
		}
	}
}

func regexpCompileForBench(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
