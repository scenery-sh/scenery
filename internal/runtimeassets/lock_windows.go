//go:build windows

package runtimeassets

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func acquireAssetLock(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open runtime asset lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	for {
		var overlapped windows.Overlapped
		err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			return func() {
				var unlock windows.Overlapped
				_ = windows.UnlockFileEx(handle, 0, 1, 0, &unlock)
				_ = file.Close()
			}, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION && err != windows.ERROR_SHARING_VIOLATION {
			_ = file.Close()
			return nil, fmt.Errorf("lock runtime asset: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("lock runtime asset: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
