//go:build unix

package dirlisting

import (
	"io/fs"
	"syscall"

	"scenery.sh/internal/filemeta"
)

// stampOf identifies a directory by device, inode, size and its modification
// and status-change times. Without a status-change time the directory is not
// identified and its listing is never reused.
func stampOf(info fs.FileInfo) (stamp, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return stamp{}, false
	}
	identity, ok := filemeta.Read(info)
	if !ok {
		return stamp{}, false
	}
	return stamp{device: uint64(stat.Dev), inode: stat.Ino, size: info.Size(), modified: info.ModTime().UnixNano(), change: identity.ChangeTimeNano}, true
}
