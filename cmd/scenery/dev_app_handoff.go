package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"scenery.sh/internal/build"
	runtime "scenery.sh/runtime/host"
)

// appStartPlan retains the exact executable and environment of a successful
// generation. Rollback must not re-read edited dotenv/config/source files.
type appStartPlan struct {
	request     devProcessStartRequest
	result      *build.Result
	metadata    json.RawMessage
	apiEncoding json.RawMessage
	assistants  *assistantStage
}

func preflightAppStart(ctx context.Context, plan *appStartPlan) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := commandTreeContext(ctx, plan.request.Command, runtime.RuntimePreflightFlag)
	command.Dir, command.Env = plan.request.Dir, plan.request.Env
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
	return validateAppPreflight(proof.buffer.Bytes(), plan.result)
}

func validateAppPreflight(data []byte, result *build.Result) error {
	proof, err := runtime.DecodeRuntimePreflight(data)
	if err != nil {
		return fmt.Errorf("candidate runtime handshake: %w", err)
	}
	if result == nil || result.Contract == nil || result.Contract.Manifest == nil || result.Target == nil || result.BuildInput == nil {
		return fmt.Errorf("candidate runtime build identity is unavailable")
	}
	if proof.RuntimeABI != runtime.ContractRuntimeABI ||
		proof.ContractRevision != result.Contract.Manifest.ContractRevision ||
		proof.ImplementationRevision != result.ImplementationRevisions[result.Target.Name] ||
		proof.BuildInputDigest != result.BuildInput.Digest || proof.GoTarget != result.Target.Name {
		return fmt.Errorf("candidate runtime does not match the supervisor's prepared build; the running generation was not replaced")
	}
	return nil
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

// replaceAppGeneration never starts a generation until its predecessor has
// stopped. A failed start may return a live process when shutdown could not be
// confirmed; that process remains owned and explicitly prevents rollback.
func replaceAppGeneration(ctx context.Context, previous *runningApp, candidate *appStartPlan, stop func(*runningApp) error, start func(context.Context, *appStartPlan) (*runningApp, error)) (*runningApp, bool, error) {
	if previous != nil {
		if err := stop(previous); err != nil {
			return previous, false, fmt.Errorf("stop previous runtime before replacement: %w", err)
		}
	}
	current, err := start(ctx, candidate)
	if err == nil || current != nil || previous == nil || previous.launch == nil || ctx.Err() != nil {
		return current, false, err
	}
	restored, restoreErr := start(ctx, previous.launch)
	if restoreErr != nil {
		return restored, false, errors.Join(err, fmt.Errorf("restore previous runtime: %w", restoreErr))
	}
	return restored, true, fmt.Errorf("candidate startup failed; restored the previous runtime: %w", err)
}
