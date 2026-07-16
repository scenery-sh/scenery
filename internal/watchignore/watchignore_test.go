package watchignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchIgnoreMatcherUsesGitignoreRules(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", strings.Join([]string{
		"/x/",
		"/var/",
		"node_modules",
		"*.log",
		"!important.log",
		"/build/*",
		"!/build/keep.go",
		"nested/**/cache/",
		"",
	}, "\n"))
	ignore := New(root)

	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{path: "x", isDir: true, want: true},
		{path: "x/file.go", want: true},
		{path: "var", isDir: true, want: true},
		{path: "apps/ui/node_modules", isDir: true, want: true},
		{path: "apps/ui/node_modules/pkg/index.js", want: true},
		{path: "debug.log", want: true},
		{path: "important.log", want: false},
		{path: "build/drop.go", want: true},
		{path: "build/keep.go", want: false},
		{path: "nested/a/b/cache", isDir: true, want: true},
		{path: "nested/a/b/cache/file.go", want: true},
		{path: "src/api.go", want: false},
	}
	for _, tt := range tests {
		if got := ignore.Ignored(tt.path, tt.isDir); got != tt.want {
			t.Fatalf("ignored(%q, dir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestWatchIgnoreMatcherUsesConfiguredRulesBeforeGitignoreNegations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/"]
		}
	}`)
	writeWatchFile(t, root, ".gitignore", "!reference/\n")
	ignore := New(root)

	if got := ignore.Ignored("reference", true); !got {
		t.Fatalf("ignored(reference dir) = %v, want true", got)
	}
	if got := ignore.Ignored("reference/api.go", false); !got {
		t.Fatalf("ignored(reference/api.go) = %v, want true", got)
	}
	if got := ignore.Ignored("kept/api.go", false); got {
		t.Fatalf("ignored(kept/api.go) = %v, want false", got)
	}
}

func TestWatchIgnoreMatcherNegatedDirectoryDoesNotUnignoreContents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "*\n!/foo/\n")
	ignore := New(root)

	if got := ignore.Ignored("foo", true); got {
		t.Fatalf("ignored(foo dir) = %v, want false", got)
	}
	if got := ignore.Ignored("foo/a.go", false); !got {
		t.Fatalf("ignored(foo/a.go) = %v, want true", got)
	}
}

func BenchmarkWatchIgnoreMatcher(b *testing.B) {
	root := b.TempDir()
	writeWatchFile(b, root, ".gitignore", strings.Join([]string{
		"node_modules", "dist/", "*.log", "!important.log", "/build/*",
		"nested/**/cache/", ".DS_Store", "coverage", "*.tmp", "/.scenery/",
	}, "\n"))
	writeWatchFile(b, root, "apps/ui/.gitignore", "dist\n*.local\n")
	ignore := New(root)
	ignore.LoadDir("apps/ui")
	paths := []string{
		"src/api.go", "apps/ui/src/components/button/index.tsx",
		"apps/ui/node_modules/react/index.js", "internal/deep/path/to/handler.go",
		"nested/a/b/cache/entry", "important.log", "apps/ui/dist/main.js",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, path := range paths {
			ignore.Ignored(path, false)
		}
	}
}

func writeWatchFile(t testing.TB, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
