package watchignore

import (
	"reflect"
	"strings"
	"testing"
)

func TestWatchIgnorePathStoragePreservesSegments(t *testing.T) {
	paths := []string{"", "a", "a/", "/a", "a//b", "é/žluťoučký.go", "invalid/\xff.go"}
	for _, depth := range []int{15, 16, 17, 32, 33, 100} {
		paths = append(paths, strings.Repeat("dir/", depth-1)+"file.go")
	}
	for _, path := range paths {
		var storage [16]string
		if got, want := splitWatchSegments(path, storage[:0]), strings.Split(path, "/"); !reflect.DeepEqual(got, want) {
			t.Fatalf("segments(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestWatchIgnoreParentMemoizationPreservesDeepDecisions(t *testing.T) {
	m := &Matcher{parents: make(map[string]parentIgnore)}
	for _, pattern := range []string{"**/cache/", "**/*.log", "!**/keep.log", "a/**/generated/*", "a/**/generated/keep.go"} {
		rule, ok := parseWatchIgnoreRule("", pattern)
		if !ok {
			t.Fatal(pattern)
		}
		m.gitRules = append(m.gitRules, rule)
	}
	for _, depth := range []int{1, 15, 16, 17, 32, 33, 100} {
		for _, suffix := range []string{"x.go", "x.log", "keep.log", "cache/x.go", "generated/keep.go", "žluťoučký.go", "invalid-\xff.go"} {
			path := "a/" + strings.Repeat("dir/", depth) + suffix
			for range 2 {
				if got, want := m.IgnoredEntry(path, false), m.Ignored(path, false); got != want {
					t.Fatalf("IgnoredEntry(%q) = %t, full-path = %t", path, got, want)
				}
			}
		}
	}
}
