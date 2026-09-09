//go:build unix

package stateupgrade

import (
	"os"
	"syscall"
)

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid() && (!info.Mode().IsRegular() || stat.Nlink == 1)
}
