//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func makeHarnessTelemetryFIFO(path string) error {
	return syscall.Mkfifo(path, 0600)
}

// Nonblocking open observes each owned CLI's input rendezvous without releasing
// data, and keeps failure/cancellation from stranding a writer after CLI exit.
func openHarnessTelemetryFIFO(ctx context.Context, path string, exited <-chan struct{}) (*os.File, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, syscall.ENXIO) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-exited:
			return nil, fmt.Errorf("telemetry export exited before its selected FIFO opened")
		case <-ticker.C:
		}
	}
}
