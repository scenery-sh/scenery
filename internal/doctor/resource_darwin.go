//go:build darwin

package doctor

import (
	"context"

	"golang.org/x/sys/unix"
)

func (defaultResourceProbe) Memory(context.Context) (MemoryInfo, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return MemoryInfo{}, err
	}
	return MemoryInfo{TotalBytes: total}, nil
}
