package compiler

import (
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceMatchGlobSegment is the recursive matcher that matchGlobSegment
// replaced. It is kept here only as the differential oracle for the rewrite:
// the iterative version must agree with it on every input. Walking the strings
// by rune boundary keeps the oracle independent without allocating fresh rune
// slices and memo maps for every comparison.
func referenceMatchGlobSegment(pattern, value string) bool {
	if pattern == "" {
		return value == ""
	}
	patternRune, patternWidth := utf8.DecodeRuneInString(pattern)
	if patternRune == '*' {
		if referenceMatchGlobSegment(pattern[patternWidth:], value) {
			return true
		}
		if value == "" {
			return false
		}
		_, valueWidth := utf8.DecodeRuneInString(value)
		return referenceMatchGlobSegment(pattern, value[valueWidth:])
	}
	if value == "" {
		return false
	}
	valueRune, valueWidth := utf8.DecodeRuneInString(value)
	return (patternRune == '?' || patternRune == valueRune) &&
		referenceMatchGlobSegment(pattern[patternWidth:], value[valueWidth:])
}

func TestMatchGlobSegmentMatchesReferenceImplementation(t *testing.T) {
	t.Parallel()

	// Exhaustive over a small alphabet that includes both metacharacters, a
	// multi-byte rune, and ordinary letters.
	alphabet := []rune{'a', 'b', '*', '?', 'é'}
	var build func(prefix []rune, depth int, emit func(string))
	build = func(prefix []rune, depth int, emit func(string)) {
		emit(string(prefix))
		if depth == 0 {
			return
		}
		for _, r := range alphabet {
			build(append(prefix, r), depth-1, emit)
		}
	}
	var words []string
	build(nil, 4, func(s string) { words = append(words, s) })

	checked := 0
	for _, pattern := range words {
		for _, value := range words {
			want := referenceMatchGlobSegment(pattern, value)
			got := matchGlobSegment(pattern, value)
			if got != want {
				t.Fatalf("matchGlobSegment(%q, %q) = %v, reference = %v", pattern, value, got, want)
			}
			checked++
		}
	}
	const wantComparisons = 609961
	if checked != wantComparisons {
		t.Fatalf("compared %d pairs, want the exhaustive %d", checked, wantComparisons)
	}
}

func TestMatchGlobSegmentMatchesReferenceOnRandomInput(t *testing.T) {
	t.Parallel()

	// Longer randomized inputs reach the backtracking paths that short
	// exhaustive words do not, including runs of consecutive stars.
	source := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune{'a', 'b', 'c', '*', '?', '.', 'é', '中'}
	randomWord := func(maxLen int) string {
		runes := make([]rune, source.IntN(maxLen))
		for i := range runes {
			runes[i] = alphabet[source.IntN(len(alphabet))]
		}
		return string(runes)
	}
	for range 200000 {
		pattern, value := randomWord(12), randomWord(12)
		if got, want := matchGlobSegment(pattern, value), referenceMatchGlobSegment(pattern, value); got != want {
			t.Fatalf("matchGlobSegment(%q, %q) = %v, reference = %v", pattern, value, got, want)
		}
	}
}

func BenchmarkMatchesAnyGlobPerFile(b *testing.B) {
	// The workspace-revision walk shape: constant patterns, one value per file.
	patterns := []string{"**/*.go", "go.mod", "go.sum", "**/*.scn", "**/*.sql", "internal/**/*.json"}
	matcher := newGlobMatcher(patterns)
	values := []string{
		"internal/agent/router.go",
		"apps/console/src/main.tsx",
		"go.mod",
		"internal/spec/testdata/fixture.json",
		"cmd/scenery/deeply/nested/path/to/file_test.go",
	}
	// The pre-change path: split every pattern per file and match segments with
	// the memoized recursion.
	referenceMatchesAnyGlob := func(patterns []string, value string) bool {
		var referenceSegments func(pattern, value []string) bool
		referenceSegments = func(pattern, value []string) bool {
			if len(pattern) == 0 {
				return len(value) == 0
			}
			if pattern[0] == "**" {
				for index := 0; index <= len(value); index++ {
					if referenceSegments(pattern[1:], value[index:]) {
						return true
					}
				}
				return false
			}
			if len(value) == 0 {
				return false
			}
			return referenceMatchGlobSegment(pattern[0], value[0]) && referenceSegments(pattern[1:], value[1:])
		}
		for _, pattern := range patterns {
			if referenceSegments(splitSlashForBench(pattern), splitSlashForBench(value)) {
				return true
			}
		}
		return false
	}
	b.Run("before", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			referenceMatchesAnyGlob(patterns, values[i%len(values)])
		}
	})
	b.Run("split-per-call", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			matchesAnyGlob(patterns, values[i%len(values)])
		}
	})
	b.Run("pre-split", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			matcher.matches(values[i%len(values)])
		}
	})
}

func splitSlashForBench(value string) []string {
	return strings.Split(filepath.ToSlash(value), "/")
}
