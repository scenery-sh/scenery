package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/rotatinglog"
	"scenery.sh/internal/telemetryreport"
)

// The dev-process lane exercises production-size segments outside fast tests.
func runHarnessSupervisorLogProof() (map[string]any, error) {
	home, err := os.MkdirTemp("", "scenery-supervisor-logs-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(home) }()
	dir := filepath.Join(home, "agent", "dev")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "supervisor.log")
	writer, err := rotatinglog.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = writer.Close() }()
	encoded := json.NewEncoder(writer)
	payload := strings.Repeat("bounded process output ", 1024)
	for range 2000 {
		if err := encoded.Encode(map[string]any{"data": map[string]any{"type": "process.output", "time": time.Now().UTC(), "app": map[string]string{"root": "/fixture", "name": "fixture"}, "data": map[string]string{"source": "service", "output": payload}}}); err != nil {
			return nil, err
		}
	}
	var total int64
	segments := 0
	for _, segment := range rotatinglog.Segments(path) {
		info, err := os.Stat(segment)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Size() > rotatinglog.SegmentBytes {
			return nil, fmt.Errorf("oversized supervisor segment: %d", info.Size())
		}
		total += info.Size()
		segments++
	}
	if segments != rotatinglog.Backups+1 || total > int64((rotatinglog.Backups+1)*rotatinglog.SegmentBytes) {
		return nil, fmt.Errorf("retained segments=%d bytes=%d", segments, total)
	}
	report, err := telemetryreport.Build(telemetryreport.Options{AgentHome: home})
	if err != nil {
		return nil, err
	}
	if report.Builds.Sessions != 1 || report.Sources.SupervisorRotated != 1 {
		return nil, fmt.Errorf("rotated report coverage: %+v", report.Sources)
	}
	return map[string]any{"segments": segments, "retained_bytes": total, "one_session": true}, nil
}
