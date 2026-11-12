//go:build darwin

package main

import "golang.org/x/sys/unix"

// cloneFileOrTree clones a file, a symlink or a whole directory tree
// on a filesystem that shares blocks between clones, such as APFS. The clone
// is independent: later writes to either side do not reach the other.
func cloneFileOrTree(source, destination string) error {
	return unix.Clonefile(source, destination, unix.CLONE_NOFOLLOW)
}
