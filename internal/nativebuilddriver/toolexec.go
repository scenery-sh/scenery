package nativebuilddriver

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RunToolExec records one actual Go compiler or linker invocation and then
// forwards it unchanged. recordRoot belongs to one private bootstrap.
func RunToolExec(recordRoot string, argv []string, environment []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if recordRoot == "" || len(argv) < 1 {
		return fmt.Errorf("native build recorder requires a root and wrapped Go tool")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	actionDir, err := os.MkdirTemp(filepath.Join(recordRoot, "actions"), "action-")
	if err != nil {
		return err
	}
	record := ToolRecord{
		Protocol:  ProtocolVersion,
		ID:        filepath.Base(actionDir),
		Tool:      argv[0],
		Argv:      append([]string(nil), argv[1:]...),
		CWD:       cwd,
		StartedAt: time.Now().UTC(),
		Files:     map[int]FileCopy{},
	}
	for index, argument := range record.Argv {
		path := argument
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() {
			copy, copyErr := CopyRegular(path, filepath.Join(actionDir, "input-"+strconv.Itoa(index)))
			if copyErr != nil {
				return copyErr
			}
			record.Files[index] = copy
		}
	}
	started := time.Now()
	command := exec.Command(record.Tool, record.Argv...)
	command.Dir, command.Env = cwd, environment
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	runErr := command.Run()
	record.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	if command.ProcessState != nil {
		record.ExitCode = command.ProcessState.ExitCode()
	} else {
		record.ExitCode = 125
	}
	if runErr == nil {
		if output := toolFlagValue(record.Argv, "-o"); output != "" {
			if !filepath.IsAbs(output) {
				output = filepath.Join(cwd, output)
			}
			copy, copyErr := CopyRegular(output, filepath.Join(actionDir, "output"))
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

func toolFlagValue(args []string, name string) string {
	for index, argument := range args {
		if argument == name && index+1 < len(args) {
			return args[index+1]
		}
		if value, ok := strings.CutPrefix(argument, name+"="); ok {
			return value
		}
	}
	return ""
}
