package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRootFollowsAnExecutableRelativeLinkedRoot(t *testing.T) {
	previous := linkedRepoRoot
	t.Cleanup(func() { linkedRepoRoot = previous })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		t.Fatal(err)
	}
	linkedRepoRoot = ExecutableRelativeRoot + "../../../../source/0123"
	want := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", "..", "..", "source", "0123"))
	if got := RepoRoot(); got != want {
		t.Fatalf("RepoRoot() = %q, want %q", got, want)
	}
	linkedRepoRoot = "/absolute/./source"
	if got := RepoRoot(); got != "/absolute/source" {
		t.Fatalf("absolute linked root = %q", got)
	}
}
