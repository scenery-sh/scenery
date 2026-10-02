package compiler

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobMatcherPathSegmentationPreservesMatches(t *testing.T) {
	patterns := [][]string{
		nil,
		{"**"},
		{"**/*.go", "go.mod"},
		{"a/**/b", "?/é/*", "**/中?.sql"},
		{"a/**", "cache/**", "a/*/**"},
		{"a/", "/a", "a//b", "*\xff*"},
	}
	paths := []string{"", "a", "a/", "/a", "a//b", "a/b", "a/c/b", "cache/nested/file.go", "中é.sql", "x/é/y", "x\xffy"}
	for _, depth := range []int{15, 16, 17, 32, 33, 100} {
		paths = append(paths, strings.Repeat("a/", depth)+"b", strings.Repeat("a/", depth)+"file.go")
	}
	for _, alternatives := range patterns {
		matcher := newGlobMatcher(alternatives)
		for _, path := range paths {
			// The original allocating split is the independent segmentation
			// oracle; the segment matcher and pattern grammar stay unchanged.
			segments := strings.Split(filepath.ToSlash(path), "/")
			want := false
			for _, pattern := range matcher {
				want = want || matchGlobSegments(pattern, segments)
			}
			if got := matcher.matches(path); got != want {
				t.Fatalf("patterns=%q path=%q: got %t, want %t", alternatives, path, got, want)
			}
			covered := false
			for _, pattern := range matcher {
				if len(pattern) != len(segments)+1 || pattern[len(pattern)-1] != "**" {
					continue
				}
				match := true
				for index, segment := range segments {
					match = match && pattern[index] != "**" && matchGlobSegment(pattern[index], segment)
				}
				covered = covered || match
			}
			if got := matcher.coversDirectory(path); got != covered {
				t.Fatalf("directory patterns=%q path=%q: got %t, want %t", alternatives, path, got, covered)
			}
		}
	}
}
