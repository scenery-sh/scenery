//go:build darwin || linux

package appconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockEnvironment serializes writers of one environment across processes with
// an exclusive flock on environment-locks/<env>.lock. Each call opens its own
// file description so goroutines never share lock ownership. The lock file is
// opened by path with O_NOFOLLOW rather than through os.Root: concurrent
// O_CREATE opens of one name through os.Root fail with ENOENT on darwin
// (Go 1.27).
func lockEnvironment(ctx context.Context, root *os.Root, environment string) (func(), error) {
	if err := mkdirs(root, "environment-locks"); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root.Name(), "environment-locks", environment+".lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
