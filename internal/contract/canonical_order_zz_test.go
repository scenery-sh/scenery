package contract

import (
	"math/rand/v2"
	"regexp"
	"sort"
	"testing"
	"unicode/utf16"
)

// referenceContractUTF16Less is the always-encode comparison that
// contractUTF16Less's ASCII fast path replaced. Contract wire bytes depend on
// object-key order, so the two must agree on every input.
func referenceContractUTF16Less(left, right string) bool {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for index := 0; index < len(a) && index < len(b); index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return len(a) < len(b)
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
		runes := make([]rune, source.IntN(6))
		for i := range runes {
			runes[i] = alphabet[source.IntN(len(alphabet))]
		}
		return string(runes)
	}
	for range 300000 {
		left, right := word(), word()
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
