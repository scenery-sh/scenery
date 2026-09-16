// Package dirlisting reuses directory listings between walks of trees whose
// membership rarely changes, such as the repeated scans of a development
// watcher.
//
// A listing is reused only while the directory's identity, modification time
// and size are those observed immediately before and after it was read, and
// only when the directory had not been modified for longer than the timestamp
// granularity of common filesystems when it was read. A membership change in
// the same timestamp tick as a listing is therefore never hidden: such a
// directory is read again until its modification time is old enough.
package dirlisting

import (
	"io/fs"
	"os"
	"sync"
	"time"
)

// stableAge exceeds the modification-time granularity of common filesystems.
const stableAge = 2 * time.Second

type stamp struct {
	device, inode uint64
	modified      int64
	size          int64
}

type listing struct {
	stamp   stamp
	entries []fs.DirEntry
}

var listings sync.Map

// ReadDir returns the entries of the directory at path sorted by name, as
// os.ReadDir does. Callers must name one directory by one path spelling to
// benefit from reuse. An entry's Info reads the entry's current metadata.
func ReadDir(path string) ([]fs.DirEntry, error) {
	entries, _, err := ReadDirObserved(path)
	return entries, err
}

// ReadDirObserved is ReadDir that also reports whether an earlier listing was
// reused instead of reading the directory.
func ReadDirObserved(path string) ([]fs.DirEntry, bool, error) {
	before, err := os.Lstat(path)
	current, identified := stamp{}, false
	if err == nil && before.IsDir() {
		current, identified = stampOf(before)
	}
	if identified {
		if value, ok := listings.Load(path); ok && value.(listing).stamp == current {
			return append([]fs.DirEntry(nil), value.(listing).entries...), true, nil
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil || !identified {
		listings.Delete(path)
		return entries, false, err
	}
	after, statErr := os.Lstat(path)
	if statErr != nil || !after.IsDir() {
		listings.Delete(path)
		return entries, false, nil
	}
	if confirmed, ok := stampOf(after); ok && confirmed == current && time.Since(before.ModTime()) > stableAge {
		listings.Store(path, listing{stamp: current, entries: append([]fs.DirEntry(nil), entries...)})
	} else {
		listings.Delete(path)
	}
	return entries, false, nil
}
