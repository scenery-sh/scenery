//go:build unix

package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockWorkspace(root string) (func(), error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, ".scenery-workspace.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock build workspace: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// tryLockWorkspace takes the workspace lock only when nobody holds it. A held
// lock returns held=true and no unlock; the caller must not touch the
// workspace. The lock file is opened but never created, so probing an unknown
// directory cannot create one.
func tryLockWorkspace(root string) (unlock func(), held bool, err error) {
	file, err := os.OpenFile(filepath.Join(root, ".scenery-workspace.lock"), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("probe build workspace lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, false, nil
}
