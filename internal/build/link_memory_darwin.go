//go:build darwin

package build

import "golang.org/x/sys/unix"

// hostPhysicalMemory returns the host's physical memory in bytes, or zero
// when it is unknown.
func hostPhysicalMemory() uint64 {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return total
}
