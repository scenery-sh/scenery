package main

import (
	"context"
	"os"
	"time"
)

var (
	parentCheckInterval = time.Second
	parentPID           = os.Getppid
)

func startParentMonitor(ctx context.Context, cancel context.CancelFunc) func() {
	initial := parentPID()
	if initial <= 1 {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(parentCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if parentMonitorShouldCancel(initial, parentPID()) {
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		close(done)
	}
}

func parentMonitorShouldCancel(initial, current int) bool {
	return initial > 1 && current > 0 && current != initial
}
