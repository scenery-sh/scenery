package build

import (
	"context"
	"sync"
	"time"
)

// Step describes one measured interval. Callers project it into their existing
// event stream; this package neither stores traces nor adds a second logger.
type Step struct {
	Name      string
	StartedAt time.Time
	Duration  time.Duration
	Cache     string
	Reason    string
	OK        bool
}

type traceKey struct{}

func WithTrace(ctx context.Context, emit func(Step)) context.Context {
	var mu sync.Mutex
	return context.WithValue(ctx, traceKey{}, func(step Step) {
		if emit != nil {
			mu.Lock()
			defer mu.Unlock()
			emit(step)
		}
	})
}

func finishStep(ctx context.Context, name string, started time.Time, cache, reason string, err error) {
	if emit, ok := ctx.Value(traceKey{}).(func(Step)); ok && emit != nil {
		emit(Step{Name: name, StartedAt: started, Duration: time.Since(started), Cache: cache, Reason: reason, OK: err == nil})
	}
}

func observeBuild[T any](ctx context.Context, name string, fn func() (T, error)) (T, error) {
	started := time.Now()
	value, err := fn()
	finishStep(ctx, name, started, "not_applicable", "executed", err)
	return value, err
}

func observeBuildAction(ctx context.Context, name string, fn func() error) error {
	_, err := observeBuild(ctx, name, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}
