package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
)

type cliTelemetryApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cliTelemetryRecord struct {
	At         time.Time        `json:"at"`
	Command    string           `json:"command"`
	DurationMS int64            `json:"duration_ms"`
	ExitCode   int              `json:"exit_code"`
	Version    string           `json:"version"`
	Mode       string           `json:"mode"`
	App        *cliTelemetryApp `json:"app,omitempty"`
}

func recordCLITelemetry(record cliTelemetryRecord) {
	path, err := cliTelemetryPath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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

func cliTelemetryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".scenery", "telemetry.jsonl"), nil
}

// telemetryInvocationApp resolves only stable configured identity. App roots
// and raw arguments never enter the telemetry stream.
func telemetryInvocationApp(args []string) *cliTelemetryApp {
	return telemetryInvocationAppFrom(args, ".")
}

func telemetryInvocationAppFrom(args []string, start string) *cliTelemetryApp {
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		if argument == "--app-root" {
			if index+1 >= len(args) {
				return nil
			}
			start = args[index+1]
			break
		}
		if strings.HasPrefix(argument, "--app-root=") {
			start = strings.TrimPrefix(argument, "--app-root=")
			break
		}
	}
	root, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil || root == "" {
		return nil
	}
	return &cliTelemetryApp{ID: cfg.AppID(), Name: cfg.Name}
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
