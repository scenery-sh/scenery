package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/workspacetx"
)

// This named process probe holds a real live transaction over invalid partial
// bytes, then publishes one coherent edit and removes its own app source.
// Retained state is deliberately separate from the runtime's lifetime.
func harnessSourceRecovery(ctx context.Context, repoRoot, appRoot, home, log, source string, before harnessProcessModelResponse,
	call func(context.Context, string, string) (harnessProcessModelResponse, error),
	waitFor func(string, string, string) (harnessProcessModelResponse, time.Duration, error),
) (map[string]any, error) {
	info, err := os.Stat(log)
	if err != nil {
		return nil, err
	}
	offset := info.Size()
	original, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(appRoot, ".scenery", "transactions", "change-source-probe")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(filepath.Dir(directory), "change.lock")
	lock, _ := workspacetx.NewArtifacts(directory, "")
	encoded, err := json.Marshal(lock)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(lockPath, encoded, 0o600); err != nil {
		return nil, err
	}
	defer func() {
		_ = os.Remove(lockPath)
		_ = os.RemoveAll(directory)
	}()
	if err := os.WriteFile(source, []byte("package echo\nfunc partial("), 0o600); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitLog(ctx, log, offset, "build.deferred"); err != nil {
		return nil, err
	}
	response, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || response.Message != before.Message || response.Generation != before.Generation || response.Build != before.Build {
		return nil, fmt.Errorf("live transaction disturbed the published generation: %+v, %v", response, err)
	}
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.Type == "build.error" || (event.Type == "build.step" && event.Data.Name == "build.request") {
			return nil, fmt.Errorf("live transaction produced a build instead of waiting: %+v", event)
		}
	}
	final := strings.Replace(string(original), `text.Label("echo-lost", input.Message)`, `text.Label("echo-final", input.Message)`, 1)
	if final == string(original) {
		return nil, fmt.Errorf("source recovery fixture did not contain the intended edit")
	}
	if err := os.WriteFile(source, []byte(final), 0o600); err != nil {
		return nil, err
	}
	if err := os.Remove(lockPath); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(directory); err != nil {
		return nil, err
	}
	after, latency, err := waitFor("/echo", `{"message":"hi"}`, "echo-final|hi")
	if err != nil || after.Generation <= before.Generation || after.Build == before.Build {
		return nil, fmt.Errorf("transaction release did not publish the final source: %+v, %v", after, err)
	}
	if err := harnessWaitBuildRequest(ctx, log, offset, true); err != nil {
		return nil, err
	}
	if err := harnessAssertWatchBuildCount(ctx, log, offset, 1); err != nil {
		return nil, err
	}
	events, err = harnessWatchEvents(log, offset)
	if err != nil {
		return nil, err
	}
	deferred, resumed := 0, 0
	for _, event := range events {
		switch event.Type {
		case "build.deferred":
			deferred++
		case "build.resumed":
			resumed++
		case "build.error":
			return nil, fmt.Errorf("coherent transaction produced a build error: %+v", event)
		}
	}
	if deferred != 1 || resumed != 1 {
		return nil, fmt.Errorf("transaction events deferred=%d resumed=%d, want one each", deferred, resumed)
	}
	output, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "ps", "-o", "json")
	if err != nil {
		return nil, err
	}
	var status struct {
		Worktrees []struct {
			SourceFreshness string `json:"source_freshness"`
			Serving         []struct {
				Generation int    `json:"generation"`
				Build      string `json:"build_input_digest"`
			} `json:"serving"`
		} `json:"worktrees"`
	}
	if err := decodeCLIJSON(output, &status); err != nil {
		return nil, err
	}
	if len(status.Worktrees) != 1 || status.Worktrees[0].SourceFreshness != "current" || len(status.Worktrees[0].Serving) != 1 || status.Worktrees[0].Serving[0].Generation != after.Generation || status.Worktrees[0].Serving[0].Build != after.Build {
		return nil, fmt.Errorf("ps did not attest the answering generation as current: %s", output)
	}
	session, err := harnessLiveSession(ctx, home, appRoot)
	if err != nil {
		return nil, err
	}
	retained := filepath.Join(appRoot, ".scenery", "data", "source-loss-proof")
	if err := os.MkdirAll(filepath.Dir(retained), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(retained, []byte("separately owned retained data"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(appRoot, "app.scn")); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitLog(ctx, log, offset, "source.lost"); err != nil {
		return nil, err
	}
	if !harnessWaitProcessExit(session.OwnerPID, 15*time.Second) || !harnessWaitProcessExit(after.Host, 15*time.Second) || !harnessWaitProcessExit(after.PID, 15*time.Second) {
		return nil, fmt.Errorf("missing source left the verified owner or its current processes running")
	}
	data, err := os.ReadFile(retained)
	if err != nil || string(data) != "separately owned retained data" {
		return nil, fmt.Errorf("source-loss shutdown changed retained data: %q, %v", data, err)
	}
	return map[string]any{"transaction_deferred": deferred, "coherent_builds": 1, "generation": after.Generation, "build_input_digest": after.Build, "edit_to_successful_response_ms": latency.Milliseconds(), "source_loss_stopped_verified_processes": true, "retained_data_unchanged": true}, nil
}
