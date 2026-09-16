//go:build unix

package dirlisting

import (
	"io/fs"
	"syscall"
)

func stampOf(info fs.FileInfo) (stamp, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return stamp{}, false
	}
	return stamp{device: uint64(stat.Dev), inode: stat.Ino, modified: info.ModTime().UnixNano(), size: info.Size()}, true
}
