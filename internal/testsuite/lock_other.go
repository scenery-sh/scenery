//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package testsuite

import (
	"context"
	"os"
)

func lockCache(_ context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return func() { _ = file.Close() }, nil
}
