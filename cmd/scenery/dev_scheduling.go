package main

import (
	"context"
	"time"

	"scenery.sh/internal/build"
)

// recordDevScheduling runs before each build request because children inherit
// the supervisor's scheduling policy, and a launcher may apply it again later.
// Platforms without an observable policy return no reason and record nothing.
func recordDevScheduling(ctx context.Context, clearPolicy func() (string, error)) {
	started := time.Now()
	reason, err := clearPolicy()
	if reason == "" {
		return
	}
	build.RecordStep(ctx, build.Step{
		Name: "process.scheduling", StartedAt: started, Duration: time.Since(started),
		Cache: "not_applicable", Reason: reason, OK: err == nil,
	})
}
