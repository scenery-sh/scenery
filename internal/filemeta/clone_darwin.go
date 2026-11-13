//go:build darwin

package filemeta

import "golang.org/x/sys/unix"

// Clone makes destination, which must not exist, an independent copy of the
// file, symlink or directory tree at source that shares its blocks with
// source until either changes. It needs a filesystem that clones, such as
// APFS, and both paths on one volume.
func Clone(source, destination string) error {
	return unix.Clonefile(source, destination, unix.CLONE_NOFOLLOW)
}
