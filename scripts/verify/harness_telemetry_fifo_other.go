//go:build !darwin && !linux

package main

import (
	"context"
	"errors"
	"os"
)

func makeHarnessTelemetryFIFO(string) error {
	return errors.New("telemetry publication contention proof requires Darwin or Linux FIFO support")
}

func openHarnessTelemetryFIFO(context.Context, string, <-chan struct{}) (*os.File, error) {
	return nil, errors.New("telemetry publication contention proof requires Darwin or Linux FIFO support")
}
