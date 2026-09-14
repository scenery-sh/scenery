package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/scripts/verify/internal/nativebuilddriver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("missing wrapped Go tool")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(executable)), "bootstrap")
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	actionDir, err := os.MkdirTemp(filepath.Join(root, "actions"), "action-")
	if err != nil {
		return err
	}
	record := nativebuilddriver.ToolRecord{Protocol: nativebuilddriver.ProtocolVersion,
		ID: filepath.Base(actionDir), Tool: os.Args[1], Argv: append([]string(nil), os.Args[2:]...),
		CWD: cwd, StartedAt: time.Now().UTC(), Files: map[int]nativebuilddriver.FileCopy{}}
	for i, arg := range record.Argv {
		path := arg
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() {
			copy, copyErr := nativebuilddriver.CopyRegular(path, filepath.Join(actionDir, "input-"+strconv.Itoa(i)))
			if copyErr != nil {
				return copyErr
			}
			record.Files[i] = copy
		}
	}
	started := time.Now()
	cmd := exec.Command(record.Tool, record.Argv...)
	cmd.Dir, cmd.Env = cwd, envpolicy.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	record.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	if cmd.ProcessState != nil {
		record.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		record.ExitCode = 125
	}
	if runErr == nil {
		if output := flagValue(record.Argv, "-o"); output != "" {
			if !filepath.IsAbs(output) {
				output = filepath.Join(cwd, output)
			}
			copy, copyErr := nativebuilddriver.CopyRegular(output, filepath.Join(actionDir, "output"))
			if copyErr != nil {
				return copyErr
			}
			record.Output = &copy
		}
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(actionDir, "record.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(record.Tool), strings.Join(record.Argv, " "), runErr)
	}
	return nil
}

func flagValue(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value
		}
	}
	return ""
}
