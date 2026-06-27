//go:build unix

package main

import (
	"context"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func defaultDoctorDisk(_ context.Context, path string) (doctorDiskInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return doctorDiskInfo{}, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(abs, &stat); err != nil {
		return doctorDiskInfo{}, err
	}
	blockSize := uint64(stat.Bsize)
	return doctorDiskInfo{
		Path:       abs,
		FreeBytes:  uint64(stat.Bavail) * blockSize,
		TotalBytes: uint64(stat.Blocks) * blockSize,
	}, nil
}
