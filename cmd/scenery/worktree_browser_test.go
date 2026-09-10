package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkedWorktreeBrowserConfigIdentity(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, "primary", ".git", "worktrees", "task")
	worktree := filepath.Join(root, "task")
	for _, path := range []string{gitDir, worktree} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../primary/.git/worktrees/task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isLinkedGitWorktree(worktree) || isLinkedGitWorktree(filepath.Join(root, "primary")) {
		t.Fatal("ordinary checkout or gitdir without commondir treated as a linked worktree")
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isLinkedGitWorktree(worktree) {
		t.Fatal("linked worktree did not receive independent browser allocation")
	}
}
