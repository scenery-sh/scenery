package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type cliTelemetryRecord struct {
	At         time.Time `json:"at"`
	Command    string    `json:"command"`
	DurationMS int64     `json:"duration_ms"`
	ExitCode   int       `json:"exit_code"`
	Version    string    `json:"version"`
	Mode       string    `json:"mode"`
}

func recordCLITelemetry(record cliTelemetryRecord) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".scenery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "telemetry.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = file.Write(append(encoded, '\n'))
}

func telemetryCommand(args []string) string {
	if len(args) == 0 {
		return "help"
	}
	switch args[0] {
	case "db", "task", "storage", "validate", "worktree", "harness", "inspect", "logs", "traces", "metrics", "system", "deploy", "changes":
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			return args[0] + " " + args[1]
		}
	}
	return args[0]
}

func telemetryMode(args []string) string {
	if len(args) == 0 {
		return "oneshot"
	}
	switch args[0] {
	case "up", "worker", "console":
		return "long_running"
	case "logs":
		for _, arg := range args[1:] {
			if arg == "--follow" {
				return "long_running"
			}
		}
	}
	return "oneshot"
}
