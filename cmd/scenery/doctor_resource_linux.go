//go:build linux

package main

import (
	"context"

	"golang.org/x/sys/unix"
)

func defaultDoctorMemory(context.Context) (doctorMemoryInfo, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return doctorMemoryInfo{}, err
	}
	return doctorMemoryInfo{TotalBytes: info.Totalram * uint64(info.Unit)}, nil
}
