package appwalk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkipDirName(t *testing.T) {
	for _, name := range []string{".git", ".scenery", ".claude", "node_modules", "dist", "out"} {
		if !SkipDirName(name) {
			t.Errorf("SkipDirName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", ".", "..", "services", ".github", "ui", "distribution", "output"} {
		if SkipDirName(name) {
			t.Errorf("SkipDirName(%q) = true, want false", name)
		}
	}
}

func TestSkipDir(t *testing.T) {
	root := t.TempDir()
	mkdir := func(rel string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write := func(rel string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mkdir(".git")
	mkdir("services/api")
	mkdir("services/api/dist")
	mkdir("web/node_modules")
	mkdir(".claude/worktrees/agent-a/.git")
	mkdir("vendored/checkout")
	write("vendored/checkout/.git")
	mkdir(".github")

	tests := []struct {
		rel  string
		want bool
	}{
		{".git", true},
		{"services", false},
		{"services/api", false},
		{"services/api/dist", true},
		{"web/node_modules", true},
		{".claude", true},
		// Nested checkout with a .git directory, as in agent worktrees.
		{".claude/worktrees/agent-a", true},
		// Nested checkout marked by a .git file, as in linked git worktrees.
		{"vendored/checkout", true},
		{"vendored", false},
		{".github", false},
	}
	for _, tt := range tests {
		path := filepath.Join(root, filepath.FromSlash(tt.rel))
		if got := SkipDir(root, path); got != tt.want {
			t.Errorf("SkipDir(root, %q) = %v, want %v", tt.rel, got, tt.want)
		}
	}

	if SkipDir(root, root) {
		t.Error("SkipDir must never skip the walk root, even with a .git entry inside")
	}

	// A root whose own basename is a skip name is still walkable.
	distRoot := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(distRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if SkipDir(distRoot, distRoot) {
		t.Error("SkipDir must not skip a walk root named after a skip directory")
	}
}
