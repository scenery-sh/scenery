//go:build darwin || linux

package storagefs

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

type fileLease struct {
	file *os.File
	once sync.Once
	err  error
}

// Every lease opens a fresh file description. Sharing one descriptor would
// allow concurrent goroutines to accidentally share flock ownership.
func lockFile(ctx context.Context, root *os.Root, name string, exclusive bool) (*fileLease, error) {
	f, err := openOwned(root, name, os.O_RDWR)
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return &fileLease{file: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *fileLease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() { l.err = errors.Join(syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN), l.file.Close()) })
	return l.err
}
