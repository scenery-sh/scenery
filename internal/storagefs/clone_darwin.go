package storagefs

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func clonePayload(source *os.File, root *os.Root, name string) (*os.File, bool, error) {
	parent, err := openOwnedDirectory(root, filepath.Dir(name))
	if err != nil {
		return nil, false, err
	}
	err = unix.Fclonefileat(int(source.Fd()), int(parent.Fd()), filepath.Base(name), 0)
	closeErr := parent.Close()
	if cloneUnavailable(err) && closeErr == nil {
		return nil, false, nil
	}
	if err := errors.Join(err, closeErr); err != nil {
		return nil, false, err
	}
	f, err := openOwned(root, name, os.O_RDWR)
	return f, err == nil, err
}
