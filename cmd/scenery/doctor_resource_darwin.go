//go:build darwin

package main

import (
	"context"

	"golang.org/x/sys/unix"
)

func defaultDoctorMemory(context.Context) (doctorMemoryInfo, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return doctorMemoryInfo{}, err
	}
	return doctorMemoryInfo{TotalBytes: total}, nil
}
