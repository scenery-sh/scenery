//go:build !darwin && !linux

package appconfig

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

func lockEnvironment(context.Context, *os.Root, string) (func(), error) {
	return nil, fmt.Errorf("environment configuration writes are not supported on %s", runtime.GOOS)
}
