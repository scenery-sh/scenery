package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"scenery.sh/runtime"
)

// appStartPlan retains the exact executable and environment of a successful
// generation. Rollback must not re-read edited dotenv/config/source files.
type appStartPlan struct {
	request devProcessStartRequest
}

// preflightProcessStart runs an executable's runtime handshake with its exact
// start environment and validates the proof before the process may serve.
func preflightProcessStart(ctx context.Context, request devProcessStartRequest, validate func([]byte) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := commandTreeContext(ctx, request.Command, runtime.RuntimePreflightFlag)
	command.Dir, command.Env = request.Dir, request.Env
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	stopReader := context.AfterFunc(ctx, func() { _ = reader.Close() })
	defer stopReader()
	// Keep the handshake separate from arbitrary application init logging.
	command.ExtraFiles = []*os.File{writer}
	proof := &boundedPreflightOutput{limit: 64 << 10}
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(proof, reader)
		readDone <- err
	}()
	output := &boundedPreflightOutput{limit: 64 << 10}
	command.Stdout, command.Stderr = output, output
	runErr := command.Run()
	_ = writer.Close()
	readErr := <-readDone
	if runErr != nil {
		return fmt.Errorf("candidate runtime preflight failed: %w: %s", runErr, output.buffer.String())
	}
	if readErr != nil {
		return fmt.Errorf("read candidate runtime preflight: %w", readErr)
	}
	if proof.truncated {
		return fmt.Errorf("candidate runtime preflight exceeded its output limit")
	}
	return validate(proof.buffer.Bytes())
}

type boundedPreflightOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *boundedPreflightOutput) Write(data []byte) (int, error) {
	length := len(data)
	if remaining := w.limit - w.buffer.Len(); len(data) > remaining {
		data, w.truncated = data[:remaining], true
	}
	_, _ = w.buffer.Write(data)
	return length, nil
}

var _ io.Writer = (*boundedPreflightOutput)(nil)
