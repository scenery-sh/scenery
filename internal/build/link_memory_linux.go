//go:build linux

package build

import "golang.org/x/sys/unix"

// hostPhysicalMemory returns the host's physical memory in bytes, or zero
// when it is unknown.
func hostPhysicalMemory() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	return uint64(info.Totalram) * uint64(info.Unit)
}
