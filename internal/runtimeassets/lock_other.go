//go:build !unix && !windows

package runtimeassets

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Unsupported targets use an atomic lock directory.  This deliberately
// scoped fallback is not selected on Unix or Windows, where the platform
// provides a real interprocess file lock.  A target-specific implementation
// should replace this fallback before production support is added for it.
func acquireAssetLock(ctx context.Context, path string) (func(), error) {
	lockDir := path + ".dir"
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockDir) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create runtime asset lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("lock runtime asset: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
