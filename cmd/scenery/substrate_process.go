package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
)

type substrateLogWriters struct {
	stdout     io.Writer
	stderr     io.Writer
	stdoutPath string
	stderrPath string
	close      func() error
}

func openSubstrateLogWriters(root, kind, component string, console *runConsole) (substrateLogWriters, error) {
	dir := substrateLogDir(root, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return substrateLogWriters{}, err
	}
	base := fmt.Sprintf("%s.%s", kind, component)
	stdoutPath := filepath.Join(dir, base+".stdout.log")
	stderrPath := filepath.Join(dir, base+".stderr.log")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return substrateLogWriters{}, err
	}
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		return substrateLogWriters{}, err
	}
	stdout := io.Writer(stdoutFile)
	stderr := io.Writer(stderrFile)
	if console != nil && console.verbose {
		stdout = io.MultiWriter(stdoutFile, os.Stdout)
		stderr = io.MultiWriter(stderrFile, os.Stderr)
	}
	return substrateLogWriters{
		stdout:     stdout,
		stderr:     stderr,
		stdoutPath: stdoutPath,
		stderrPath: stderrPath,
		close: func() error {
			return errors.Join(stdoutFile.Close(), stderrFile.Close())
		},
	}, nil
}

func substrateLogDir(root, kind string) string {
	cleanRoot := filepath.Clean(root)
	if filepath.Base(cleanRoot) == kind {
		return filepath.Join(cleanRoot, "logs")
	}
	return filepath.Join(cleanRoot, ".scenery", "substrates", kind, "logs")
}

func substrateExitRecord(component string, pid int, startedAt time.Time, stdoutPath, stderrPath string, err error, state *os.ProcessState) localagent.SubstrateExit {
	exitCode := 0
	if state != nil {
		exitCode = state.ExitCode()
	} else if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
			state = exitErr.ProcessState
		} else {
			exitCode = -1
		}
	}
	record := localagent.SubstrateExit{
		Component:     component,
		PID:           pid,
		StartedAt:     startedAt,
		ExitedAt:      time.Now().UTC(),
		ExitCode:      exitCode,
		Signal:        processExitSignal(state),
		LogPath:       stderrPath,
		StdoutLogPath: stdoutPath,
		StderrLogPath: stderrPath,
	}
	if err != nil {
		record.Error = err.Error()
	}
	return record
}
