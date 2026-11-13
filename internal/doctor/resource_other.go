//go:build !unix

package doctor

import (
	"context"
	"errors"
)

var errResourceUnsupported = errors.New("unsupported platform resource probe")

func (defaultResourceProbe) Disk(context.Context, string) (DiskInfo, error) {
	return DiskInfo{}, errResourceUnsupported
}

func (defaultResourceProbe) Memory(context.Context) (MemoryInfo, error) {
	return MemoryInfo{}, errResourceUnsupported
}
