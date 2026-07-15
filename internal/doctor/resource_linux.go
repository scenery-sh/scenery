//go:build linux

package doctor

import (
	"context"

	"golang.org/x/sys/unix"
)

func (defaultResourceProbe) Memory(context.Context) (MemoryInfo, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return MemoryInfo{}, err
	}
	return MemoryInfo{TotalBytes: info.Totalram * uint64(info.Unit)}, nil
}
