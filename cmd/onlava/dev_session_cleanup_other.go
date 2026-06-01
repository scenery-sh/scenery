//go:build !linux

package main

import (
	"context"

	localagent "github.com/pbrazdil/onlava/internal/agent"
)

func stopSessionEnvProcesses(ctx context.Context, current localagent.Session, seen map[int]bool) error {
	return nil
}
