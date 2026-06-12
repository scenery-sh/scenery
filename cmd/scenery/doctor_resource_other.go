//go:build !unix && !windows

package main

import (
	"context"
	"errors"
)

var errDoctorResourceUnsupported = errors.New("unsupported platform resource probe")

func (defaultDoctorResourceProbe) Disk(context.Context, string) (doctorDiskInfo, error) {
	return doctorDiskInfo{}, errDoctorResourceUnsupported
}

func (defaultDoctorResourceProbe) Memory(context.Context) (doctorMemoryInfo, error) {
	return doctorMemoryInfo{}, errDoctorResourceUnsupported
}
