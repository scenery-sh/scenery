//go:build !unix && !windows

package main

import (
	"context"
	"errors"
)

var errDoctorResourceUnsupported = errors.New("unsupported platform resource probe")

func defaultDoctorDisk(context.Context, string) (doctorDiskInfo, error) {
	return doctorDiskInfo{}, errDoctorResourceUnsupported
}

func defaultDoctorMemory(context.Context) (doctorMemoryInfo, error) {
	return doctorMemoryInfo{}, errDoctorResourceUnsupported
}
