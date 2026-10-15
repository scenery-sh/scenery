package main

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"scenery.sh/runtime"
)

// Capture a context from a real queued task, keeping the attempt alive while
// the journey exercises public worker calls from independent goroutines.
func registerConnectionWorker() (string, <-chan context.Context, func()) {
	service := "authworker_" + strconv.Itoa(os.Getpid())
	ready, stop := make(chan context.Context, 1), make(chan struct{})
	var once sync.Once
	must(runtime.RegisterDurableTaskChecked(&runtime.DurableTask{
		Service: service, Name: "connection.worker.v1", Version: 1, HandlerRef: "connection.worker.v1",
		DefaultTimeout: 30 * time.Second, MaxAttempts: 1,
		Handler: func(ctx context.Context, _ []byte) ([]byte, error) {
			ready <- ctx
			select {
			case <-stop:
				return []byte(`{}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}))
	return service, ready, func() { once.Do(func() { close(stop) }) }
}

func connectionWorkerContext(ctx context.Context, service string, ready <-chan context.Context, exited <-chan error) context.Context {
	// Native startup opens the durable store before publishing it. Retry only
	// acquisition of the fixture's own task; token issuance has no hidden retry.
	for {
		_, err := runtime.StartDurableTask(ctx, runtime.DurableStartRequest{Service: service, TaskName: "connection.worker.v1", ID: "fixture-context", Input: map[string]any{}})
		if err == nil {
			break
		}
		select {
		case err := <-exited:
			must(err)
			panic("owned worker exited before admission")
		case <-ctx.Done():
			must(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case admitted := <-ready:
		return admitted
	case err := <-exited:
		must(err)
		panic("owned worker exited before execution")
	case <-ctx.Done():
		must(ctx.Err())
		return nil
	}
}
