//go:build unix

package agent

import (
	"os"
	"syscall"
)

func worktreeFileOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
