package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Linked Git worktrees inherit the primary checkout's config, not its browser
// origin. Git's explicit common-directory marker distinguishes them from an
// ordinary checkout or submodule without running Git during port allocation.
func isLinkedGitWorktree(root string) bool {
	marker, ok := readGitDirectoryMarker(filepath.Join(root, ".git"))
	if !ok || !strings.HasPrefix(marker, "gitdir: ") {
		return false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(marker, "gitdir: "))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	common, ok := readGitDirectoryMarker(filepath.Join(gitDir, "commondir"))
	if !ok {
		return false
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common) != filepath.Clean(gitDir)
}

func readGitDirectoryMarker(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", false
	}
	data, err := os.ReadFile(path)
	value := strings.TrimSpace(string(data))
	return value, err == nil && value != "" && !strings.ContainsAny(value, "\r\n\x00")
}
