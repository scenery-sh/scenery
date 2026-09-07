//go:build !unix

package agent

import "os"

// Worktree control requires Unix sockets and verifiable local ownership.
func worktreeFileOwned(os.FileInfo) bool { return false }
