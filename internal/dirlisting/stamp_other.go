//go:build !unix

package dirlisting

import "io/fs"

// Without a stable directory identity, listings are never reused.
func stampOf(fs.FileInfo) (stamp, bool) { return stamp{}, false }
