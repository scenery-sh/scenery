//go:build darwin || linux

package storagefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func checkOwned(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o077 != 0 || info.IsDir() != directory || (!directory && (!info.Mode().IsRegular() || stat.Nlink != 1)) {
		return fmt.Errorf("%w: expected private, current-user-owned filesystem entry", ErrOwnership)
	}
	return nil
}

func fileIdentity(info os.FileInfo) (uint64, uint64) {
	stat := info.Sys().(*syscall.Stat_t)
	return uint64(stat.Dev), uint64(stat.Ino)
}

func openExternalParent(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o022 != 0 {
		return nil, ErrOwnership
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = ErrOwnership
	}
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func openOwned(root *os.Root, name string, flags int) (*os.File, error) {
	return openOwnedEntry(root, name, flags, false)
}

func openOwnedDirectory(root *os.Root, name string) (*os.File, error) {
	return openOwnedEntry(root, name, os.O_RDONLY, true)
}

func openOwnedEntry(root *os.Root, name string, flags int, directory bool) (*os.File, error) {
	// Root prevents escape even during replacement races. Reject internal
	// symlink aliases too: they must not select another generation's tuple.
	current := ""
	for part := range strings.SplitSeq(filepath.Dir(name), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := checkDirectory(root, current); err != nil {
			return nil, err
		}
	}
	f, err := root.OpenFile(name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		err = checkOwned(info, directory)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
