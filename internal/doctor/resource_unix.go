//go:build unix

package doctor

import (
	"context"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (defaultResourceProbe) Disk(_ context.Context, path string) (DiskInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return DiskInfo{}, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(abs, &stat); err != nil {
		return DiskInfo{}, err
	}
	blockSize := uint64(stat.Bsize)
	return DiskInfo{
		Path:       abs,
		FreeBytes:  uint64(stat.Bavail) * blockSize,
		TotalBytes: uint64(stat.Blocks) * blockSize,
	}, nil
}
