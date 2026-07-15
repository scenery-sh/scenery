//go:build windows

package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

var errResourceUnsupported = errors.New("unsupported platform resource probe")

type windowsMemoryStatusEx struct {
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

func (defaultResourceProbe) Disk(_ context.Context, path string) (DiskInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return DiskInfo{}, err
	}
	ptr, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return DiskInfo{}, err
	}
	var freeToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeToCaller, &total, &free); err != nil {
		return DiskInfo{}, err
	}
	return DiskInfo{Path: abs, FreeBytes: free, TotalBytes: total}, nil
}

func (defaultResourceProbe) Memory(context.Context) (MemoryInfo, error) {
	var status windowsMemoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if r1 == 0 {
		if err != syscall.Errno(0) {
			return MemoryInfo{}, err
		}
		return MemoryInfo{}, errResourceUnsupported
	}
	return MemoryInfo{TotalBytes: status.TotalPhys}, nil
}
