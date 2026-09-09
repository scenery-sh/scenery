package storagefs

import (
	"errors"
	"os"
	"syscall"
)

// Unsupported and cross-device cloning select a byte copy. Other failures
// remain errors: permission or damaged-source failures must not be masked.
func cloneUnavailable(err error) bool {
	return errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOSYS)
}

func cloneSourceReady(source *os.File) (bool, error) {
	info, err := source.Stat()
	if err != nil {
		return false, err
	}
	if err := checkOwned(info, false); err != nil {
		return false, err
	}
	offset, err := source.Seek(0, 1)
	return offset == 0, err
}
