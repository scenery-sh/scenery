package spec

import (
	"math/rand/v2"
	"sort"
	"testing"
	"unicode/utf16"
)

// referenceLessUTF16 is the always-encode comparison that lessUTF16's ASCII fast
// path replaced. It is the differential oracle for that fast path: canonical
// JSON object-key order is a contract, so the two must agree on every input.
func referenceLessUTF16(left, right string) bool {
	a := utf16.Encode([]rune(left))
	b := utf16.Encode([]rune(right))
	for index := 0; index < len(a) && index < len(b); index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return len(a) < len(b)
}

func canonicalOrderTestWords() []string {
	// Spans the cases where UTF-16 order can diverge from byte order: ASCII,
	// Latin-1, BMP above U+E000, and astral planes encoded as surrogate pairs.
	return []string{
		"", "a", "b", "A", "Z", "_", "-", ".", "0", "9", "~", "\x7f",
		"aa", "ab", "a_", "a-", "a.", "az", "aZ",
		"é", "ü", "ß", "Ā", "ǅ",
		"中", "日本", "한국",
		"", "", "", "�", "￿",
		"\U00010000", "\U0001F600", "\U0010FFFF",
		"a", "a\U0001F600", "a", "\U0001F600a",
		"key", "key2", "Key", "kEy", "key_name", "key-name", "key.name",
	}
}

func TestLessUTF16MatchesReferenceOnBoundaryWords(t *testing.T) {
	t.Parallel()

	words := canonicalOrderTestWords()
	for _, left := range words {
		for _, right := range words {
			if got, want := lessUTF16(left, right), referenceLessUTF16(left, right); got != want {
				t.Fatalf("lessUTF16(%q, %q) = %v, reference = %v", left, right, got, want)
			}
		}
	}
}

func TestLessUTF16MatchesReferenceOnRandomInput(t *testing.T) {
	t.Parallel()

	source := rand.New(rand.NewPCG(7, 11))
	alphabet := []rune{'a', 'b', 'Z', '_', '0', 'é', '中', '', '￿', '\U0001F600', '\U0010FFFF'}
	word := func() string {
		runes := make([]rune, source.IntN(6))
		for i := range runes {
			runes[i] = alphabet[source.IntN(len(alphabet))]
		}
		return string(runes)
	}
	for range 300000 {
		left, right := word(), word()
		if got, want := lessUTF16(left, right), referenceLessUTF16(left, right); got != want {
			t.Fatalf("lessUTF16(%q, %q) = %v, reference = %v", left, right, got, want)
		}
	}
}

// TestLessUTF16ProducesIdenticalSortOrder checks the property that actually
// matters: sorting a key set must yield the same sequence either way.
func TestLessUTF16ProducesIdenticalSortOrder(t *testing.T) {
	t.Parallel()

	source := rand.New(rand.NewPCG(13, 17))
	alphabet := []rune{'a', 'b', 'Z', '_', '0', 'é', '中', '', '￿', '\U0001F600'}
	for range 3000 {
		keys := make([]string, source.IntN(12))
		for i := range keys {
			runes := make([]rune, source.IntN(5))
			for j := range runes {
				runes[j] = alphabet[source.IntN(len(alphabet))]
			}
			keys[i] = string(runes)
		}
		fast := append([]string(nil), keys...)
		slow := append([]string(nil), keys...)
		sort.SliceStable(fast, func(i, j int) bool { return lessUTF16(fast[i], fast[j]) })
		sort.SliceStable(slow, func(i, j int) bool { return referenceLessUTF16(slow[i], slow[j]) })
		for i := range fast {
			if fast[i] != slow[i] {
				t.Fatalf("sort order diverged at %d: %q vs %q (input %q)", i, fast, slow, keys)
			}
		}
	}
}

func BenchmarkLessUTF16(b *testing.B) {
	keys := []string{"app_id", "contract_revision", "kind", "schema_revision", "spec_revision", "workspace_revision"}
	b.Run("before", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			referenceLessUTF16(keys[i%len(keys)], keys[(i+1)%len(keys)])
		}
	})
	b.Run("after", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			lessUTF16(keys[i%len(keys)], keys[(i+1)%len(keys)])
		}
	})
}
