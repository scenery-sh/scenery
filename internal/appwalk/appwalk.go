// Package appwalk holds the shared skip policy for walking scenery app trees.
//
// Several CLI and runtime walkers traverse an app root looking for source
// files. They all need to avoid the same non-source directories; this package
// is the single place that decides which directories an app-tree walk skips.
// Walkers with extra local exclusions (for example docs scans skipping
// vendor or coverage) keep those additions next to the walker.
package appwalk

import (
	"os"
	"path/filepath"
)

// SkipDirName reports whether a directory basename is one of the well-known
// non-source directories that app-tree walks always skip.
func SkipDirName(name string) bool {
	switch name {
	case ".git", ".scenery", ".claude", "node_modules", "dist", "out":
		return true
	default:
		return false
	}
}

// SkipDir reports whether path should be skipped while walking the app tree
// rooted at root. It skips the SkipDirName directories and nested git
// checkouts: any directory other than root that contains a .git entry (file
// or directory), such as agent worktrees under .claude/worktrees. The root
// itself is never skipped, even though it usually contains a .git entry.
func SkipDir(root, path string) bool {
	if path == root {
		return false
	}
	if SkipDirName(filepath.Base(path)) {
		return true
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	return false
}
