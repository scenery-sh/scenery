package storagefs

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func clonePayload(source *os.File, root *os.Root, name string) (*os.File, bool, error) {
	f, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = unix.IoctlFileClone(int(f.Fd()), int(source.Fd()))
	if err == nil {
		return f, true, nil
	}
	cleanupErr := errors.Join(f.Close(), root.Remove(name))
	if cloneUnavailable(err) && cleanupErr == nil {
		return nil, false, nil
	}
	return nil, false, errors.Join(err, cleanupErr)
}
