//go:build unix

package dirlisting

import (
	"io/fs"
	"reflect"
	"syscall"
)

// stampOf identifies a directory by device, inode, size and its modification
// and status-change times. Without a status-change time the directory is not
// identified and its listing is never reused.
func stampOf(info fs.FileInfo) (stamp, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return stamp{}, false
	}
	change, ok := statusChangeTime(stat)
	if !ok {
		return stamp{}, false
	}
	return stamp{device: uint64(stat.Dev), inode: stat.Ino, size: info.Size(), modified: info.ModTime().UnixNano(), change: change}, true
}

// statusChangeTime reads the status-change time, whose field name differs
// between Unix platforms.
func statusChangeTime(stat *syscall.Stat_t) (int64, bool) {
	value := reflect.ValueOf(stat).Elem()
	for _, name := range [...]string{"Ctim", "Ctimespec"} {
		if field := value.FieldByName(name); field.IsValid() {
			if timespec, ok := field.Interface().(syscall.Timespec); ok {
				return timespec.Nano(), true
			}
		}
	}
	return 0, false
}
