package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/rotatinglog"
)

// Attach before startup phases; the caller must finish the console before closing.
func attachForegroundEventLog(console *runConsole, root string) (string, func(), error) {
	machinePaths, err := commandAgentPaths()
	if err != nil {
		return "", nil, err
	}
	paths, err := localagent.PathsForWorktree(machinePaths.Home, root)
	if err != nil {
		return "", nil, err
	}
	logPath := detachedDevLogPath(paths.ControlPaths(), root, time.Now())
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return "", nil, err
	}
	// CreateTemp prevents even simultaneous starts from sharing a session file.
	file, err := os.CreateTemp(filepath.Dir(logPath), strings.TrimSuffix(filepath.Base(logPath), ".log")+"-*.log")
	if err != nil {
		return "", nil, err
	}
	logPath = file.Name()
	if err := file.Close(); err != nil {
		return "", nil, err
	}
	writer, err := rotatinglog.Open(logPath)
	if err != nil {
		return "", nil, err
	}
	console.SetEventLog(writer)
	return logPath, func() { console.SetEventLog(nil); _ = writer.Close() }, nil
}
