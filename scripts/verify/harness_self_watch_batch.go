package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
)

// Exercise production watch timing, actual atomic saves and edits after Go
// compilation has begun. Unit tests retain fake-clock settling coverage.
func runHarnessWatchBatchProbe(ctx context.Context, root string, started detachedDevResult, current localagent.Session,
	readSession func() (localagent.Session, error), waitReplacement func(string, string) (localagent.Session, error)) (map[string]any, error) {
	sourcePath := filepath.Join(root, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	anchor := []byte(`Message: "changed:"`)
	if bytes.Count(source, anchor) != 1 {
		return nil, fmt.Errorf("watch batch source anchor is missing")
	}
	logOffset := func() (int64, error) {
		info, err := os.Stat(started.LogPath)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	batchStart, err := logOffset()
	if err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(sourcePath, bytes.Replace(source, anchor, []byte(`Message: watchPrefix()`), 1)); err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
		return nil, err
	}
	prefixPath := filepath.Join(root, "service/watch_batch.go")
	prefix := func(value string) []byte {
		return []byte(fmt.Sprintf("package service\nfunc watchPrefix() string { return %q }\n", value))
	}
	if err := os.WriteFile(prefixPath, prefix("batch:"), 0o600); err != nil {
		return nil, err
	}
	batched, err := waitReplacement(current.AppPID, "batch:handoff")
	if err != nil {
		return nil, fmt.Errorf("atomic multi-file save did not serve the completed batch: %w", err)
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, batchStart, 1); err != nil {
		return nil, err
	}
	rapidStart, err := logOffset()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(prefixPath, prefix("rapid-one:"), 0o600); err != nil {
		return nil, err
	}
	for {
		events, err := harnessWatchEvents(started.LogPath, rapidStart)
		if err != nil {
			return nil, err
		}
		compiling := false
		for _, event := range events {
			if event.Type == "phase.start" && event.Data.Title == "Compiling application source code" {
				compiling = true
			}
		}
		if compiling {
			break
		}
		if err := harnessWaitContext(ctx, 10*time.Millisecond); err != nil {
			return nil, fmt.Errorf("watch candidate never reached Go compilation: %w", err)
		}
	}
	if err := os.WriteFile(prefixPath, prefix("rapid-two:"), 0o600); err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(prefixPath, prefix("rapid-final:")); err != nil {
		return nil, err
	}
	final, err := waitReplacement(batched.AppPID, "rapid-final:handoff")
	if err != nil {
		return nil, fmt.Errorf("edit during compilation was dropped: %w", err)
	}
	generated := filepath.Join(root, "service/scenerycontract/types.gen.go")
	data, err := os.ReadFile(generated)
	if err != nil {
		return nil, err
	}
	if err := harnessAtomicWatchSave(generated, data); err != nil {
		return nil, err
	}
	if err := harnessAssertWatchBuildCount(ctx, started.LogPath, rapidStart, 2); err != nil {
		return nil, err
	}
	stable, err := readSession()
	if err != nil || stable.AppPID != final.AppPID {
		return nil, fmt.Errorf("generated publication restarted the final generation: %v", err)
	}
	return map[string]any{
		"production_settle_ms": 100, "atomic_multifile_builds": 1,
		"inflight_batch_builds": 2, "rapid_final_response": true,
		"generated_publication_no_extra_build": true,
	}, nil
}

func harnessAtomicWatchSave(path string, data []byte) error {
	temporary := path + ".save-tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	return os.Rename(temporary, path)
}

type harnessWatchEvent struct {
	Type string `json:"type"`
	Data struct {
		Title string `json:"title"`
	} `json:"data"`
}

func harnessWatchEvents(path string, offset int64) ([]harnessWatchEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if offset > int64(len(data)) {
		return nil, fmt.Errorf("owned supervisor log was truncated")
	}
	var events []harnessWatchEvent
	for _, line := range bytes.Split(data[offset:], []byte("\n")) {
		var envelope struct {
			Data harnessWatchEvent `json:"data"`
		}
		if json.Unmarshal(line, &envelope) == nil {
			events = append(events, envelope.Data)
		}
	}
	return events, nil
}

func harnessAssertWatchBuildCount(ctx context.Context, log string, offset int64, want int) error {
	// Include a complete production backup-poll interval to catch feedback.
	if err := harnessWaitContext(ctx, 2500*time.Millisecond); err != nil {
		return err
	}
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return err
	}
	count := 0
	for _, event := range events {
		if event.Type == "process.compile-start" {
			count++
		}
	}
	if count != want {
		return fmt.Errorf("completed watch batch built %d times, want %d", count, want)
	}
	return nil
}
