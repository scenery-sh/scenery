//go:build windows

package main

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

var errDoctorResourceUnsupported = errors.New("unsupported platform resource probe")

type doctorWindowsMemoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func (defaultDoctorResourceProbe) Disk(_ context.Context, path string) (doctorDiskInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return doctorDiskInfo{}, err
	}
	ptr, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return doctorDiskInfo{}, err
	}
	var freeToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeToCaller, &total, &free); err != nil {
		return doctorDiskInfo{}, err
	}
	return doctorDiskInfo{Path: abs, FreeBytes: free, TotalBytes: total}, nil
}

func (defaultDoctorResourceProbe) Memory(context.Context) (doctorMemoryInfo, error) {
	var status doctorWindowsMemoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if r1 == 0 {
		if err != syscall.Errno(0) {
			return doctorMemoryInfo{}, err
		}
		return doctorMemoryInfo{}, errDoctorResourceUnsupported
	}
	return doctorMemoryInfo{TotalBytes: status.TotalPhys}, nil
}
