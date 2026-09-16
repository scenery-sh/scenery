package watchignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A tree walk that loads each directory before its entries decides every entry
// with IgnoredEntry and IgnoreEntryPath exactly as Ignored and IgnorePath do,
// including negations of parents, directory-only, anchored and ** patterns,
// nested ignore files and configured rules.
func TestWatchIgnoreEntryDecisionsMatchFullPathDecisions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{"name":"walkapp","watch":{"ignore":["reference/","tmp/*.go"]}}`)
	writeWatchFile(t, root, ".gitignore", strings.Join([]string{
		"*.log", "!keep.log", "build/", "/dist", "docs/**/gen", "a*", "!/alpha/", "nested/*.tmp", "**/cache", "vendor", "!vendor/keep.go", "!/kept/",
	}, "\n"))
	writeWatchFile(t, root, "apps/ui/.gitignore", "dist\n!dist/keep\n*.local\n/only-here\n")
	writeWatchFile(t, root, "apps/ui/src/.gitignore", "!important.local\ngen/\n")
	for _, rel := range []string{
		".env", ".scenery.json", ".hidden/x.go", "keep.log", "debug.log", "build/out.go", "src/build/x.go", "dist/a.go", "src/dist/a.go",
		"docs/x/y/gen/a.md", "docs/gen/a.md", "alpha/main.go", "abc/main.go", "alpha/abc/x.go", "nested/x.tmp", "nested/deep/x.tmp",
		"pkg/cache/x.go", "cache/y.go", "vendor/keep.go", "vendor/other.go", "src/vendor/z.go", "reference/api.go", "tmp/a.go", "tmp/sub/b.go",
		"apps/ui/dist/keep", "apps/ui/dist/main.js", "apps/ui/src/dist/x.js", "apps/ui/a.local", "apps/ui/src/important.local", "apps/ui/src/b.local",
		"apps/ui/only-here", "apps/ui/src/only-here", "apps/ui/src/gen/x.ts", "apps/ui/gen/x.ts", "apps/ui/node_modules/react/index.js",
		"service/api.go", "service/.gitignore", "kept/x.go", "scenery_internal_processes/host/main.go",
	} {
		writeWatchFile(t, root, rel, "x")
	}
	ignore := New(root)
	checked, ignoredParents := 0, 0
	// The walk also enters directories the rules ignore, where an entry's
	// decision depends most on its parents; only the built-in decision assumes
	// a parent the walk would visit.
	var walk func(rel string, parentIgnored bool)
	walk = func(rel string, parentIgnored bool) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		ignore.LoadDirEntries(rel, entries)
		for _, entry := range entries {
			child := entry.Name()
			if rel != "" {
				child = rel + "/" + child
			}
			checked++
			if parentIgnored {
				ignoredParents++
			}
			if got, want := ignore.IgnoredEntry(child, entry.IsDir()), ignore.Ignored(child, entry.IsDir()); got != want {
				t.Errorf("IgnoredEntry(%s) = %t, Ignored = %t", child, got, want)
			}
			ignored := IgnorePath(child, entry.IsDir(), ignore)
			if got := IgnoreEntryPath(child, entry.IsDir(), ignore); !parentIgnored && got != ignored {
				t.Errorf("IgnoreEntryPath(%s) = %t, IgnorePath = %t", child, got, ignored)
			}
			if entry.IsDir() && !shouldIgnoreWatchPathBuiltin(child, true) {
				walk(child, parentIgnored || ignored)
			}
		}
	}
	walk("", false)
	if checked < 40 || ignoredParents < 5 {
		t.Fatalf("walk checked %d entries, %d below ignored directories", checked, ignoredParents)
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

// A same-size rewrite that restores the original mtime must hit the rule
// cache (proving unchanged files are not re-read per scan), while an mtime
// bump must invalidate it so edits take effect on the next scan.
func TestWatchIgnoreRuleCacheStampValidation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "dist/\n")
	writeWatchFile(t, root, ".scenery.json", `{"name":"cacheapp","watch":{"ignore":["logs/"]}}`)

	first := New(root)
	if !first.Ignored("dist", true) || !first.Ignored("logs", true) {
		t.Fatal("initial rules did not apply")
	}

	gitPath := filepath.Join(root, ".gitignore")
	gitInfo, err := os.Stat(gitPath)
	if err != nil {
		t.Fatal(err)
	}
	// Same byte length, same mtime: the stamp heuristic must reuse the old
	// parsed rules without re-reading the file.
	writeWatchFile(t, root, ".gitignore", "docs/\n")
	if err := os.Chtimes(gitPath, gitInfo.ModTime(), gitInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	cached := New(root)
	if !cached.Ignored("dist", true) {
		t.Fatal("stamp-identical rewrite must reuse cached gitignore rules")
	}
	if cached.Ignored("docs", true) {
		t.Fatal("stamp-identical rewrite must not surface the new rules yet")
	}

	if err := os.Chtimes(gitPath, gitInfo.ModTime().Add(2*time.Second), gitInfo.ModTime().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	fresh := New(root)
	if fresh.Ignored("dist", true) {
		t.Fatal("mtime bump must drop the stale cached gitignore rules")
	}
	if !fresh.Ignored("docs", true) {
		t.Fatal("mtime bump must load the edited gitignore rules")
	}

	configPath := filepath.Join(root, ".scenery.json")
	configInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, ".scenery.json", `{"name":"cacheapp","watch":{"ignore":["temp/"]}}`)
	if err := os.Chtimes(configPath, configInfo.ModTime(), configInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	cachedConfig := New(root)
	if !cachedConfig.Ignored("logs", true) || cachedConfig.Ignored("temp", true) {
		t.Fatal("stamp-identical config rewrite must reuse cached config rules")
	}
	if err := os.Chtimes(configPath, configInfo.ModTime().Add(2*time.Second), configInfo.ModTime().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	freshConfig := New(root)
	if freshConfig.Ignored("logs", true) || !freshConfig.Ignored("temp", true) {
		t.Fatal("config mtime bump must load the edited watch.ignore rules")
	}
}

// The same .gitignore file loaded under nested roots parses against
// different base dirs; the cache must not serve one root's rules to the
// other.
func TestWatchIgnoreRuleCacheNestedRootBases(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	writeWatchFile(t, parent, "sub/.gitignore", "build/\n")

	outer := New(parent)
	outer.LoadDir("sub")
	if !outer.Ignored("sub/build", true) {
		t.Fatal("parent-rooted matcher must scope sub/.gitignore rules under sub/")
	}
	if outer.Ignored("build", true) {
		t.Fatal("parent-rooted matcher must not apply sub rules at the root")
	}

	inner := New(filepath.Join(parent, "sub"))
	if !inner.Ignored("build", true) {
		t.Fatal("sub-rooted matcher must apply its own .gitignore at its root")
	}

	again := New(parent)
	again.LoadDir("sub")
	if !again.Ignored("sub/build", true) || again.Ignored("build", true) {
		t.Fatal("cache must not leak the sub-rooted base back to the parent root")
	}
}
