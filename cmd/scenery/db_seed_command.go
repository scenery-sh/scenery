package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
)

const (
	dbSeedPlanKindSQL     = "sql"
	dbSeedPlanKindCommand = "command"
)

var dbSeedCommandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)

type dbSeedCommandOutput struct {
	Stdout string
	Stderr string
}

func defaultRunDBSeedCommand(ctx context.Context, req lifecycleExecRequest) (dbSeedCommandOutput, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	req.Stdout = &stdout
	req.Stderr = &stderr
	err := defaultRunLifecycleExec(ctx, req)
	output := dbSeedCommandOutput{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return output, nil
	}
	detail := strings.TrimSpace(output.Stderr)
	if detail == "" {
		return output, err
	}
	return output, fmt.Errorf("%w: %s", err, detail)
}

func discoverDBSeedCommandPlans(appRoot string, cfg appcfg.Config) ([]dbSeedPlan, error) {
	commands := cfg.Database.Seed.Commands
	if len(commands) == 0 {
		return nil, nil
	}
	seenNames := make(map[string]struct{}, len(commands))
	plans := make([]dbSeedPlan, 0, len(commands))
	for index, command := range commands {
		name := strings.TrimSpace(command.Name)
		if !dbSeedCommandNamePattern.MatchString(name) {
			return nil, fmt.Errorf("database.seed.commands[%d].name %q must use lowercase letters, digits, hyphens, or underscores and start with a letter", index, command.Name)
		}
		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf("database.seed.commands contains duplicate name %q", name)
		}
		seenNames[name] = struct{}{}

		service := strings.TrimSpace(command.Service)
		if service == "" {
			return nil, fmt.Errorf("database.seed.commands[%d].service is required", index)
		}
		if _, ok := cfg.DatabaseService(service); !ok {
			return nil, fmt.Errorf("database.seed.commands[%d].service %q has no matching dev.services entry", index, service)
		}
		if strings.TrimSpace(command.Command) == "" {
			return nil, fmt.Errorf("database.seed.commands[%d].command is required", index)
		}
		if len(command.Inputs) == 0 {
			return nil, fmt.Errorf("database.seed.commands[%d].inputs must contain at least one file", index)
		}

		cwd, err := normalizeDBSeedCommandCWD(appRoot, command.CWD)
		if err != nil {
			return nil, fmt.Errorf("database.seed.commands[%d].cwd: %w", index, err)
		}
		inputs, inputData, err := readDBSeedCommandInputs(appRoot, command.Inputs)
		if err != nil {
			return nil, fmt.Errorf("database.seed.commands[%d].inputs: %w", index, err)
		}
		descriptor := struct {
			Name    string            `json:"name"`
			Service string            `json:"service"`
			Command string            `json:"command"`
			Inputs  []string          `json:"inputs"`
			CWD     string            `json:"cwd,omitempty"`
			Env     map[string]string `json:"env,omitempty"`
		}{
			Name: name, Service: service, Command: strings.TrimSpace(command.Command),
			Inputs: inputs, CWD: cwd, Env: command.Env,
		}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return nil, fmt.Errorf("database.seed.commands[%d]: encode fingerprint: %w", index, err)
		}
		hash := sha256.New()
		writeDBSeedCommandHashPart(hash, "config", encoded)
		for inputIndex, path := range inputs {
			writeDBSeedCommandHashPart(hash, path, inputData[inputIndex])
		}
		plans = append(plans, dbSeedPlan{
			Kind:    dbSeedPlanKindCommand,
			Service: service,
			Path:    cfg.SourceRelPath(appRoot) + "#/database/seed/commands/" + name,
			SHA256:  hex.EncodeToString(hash.Sum(nil)),
			Command: descriptor.Command,
			CWD:     cwd,
			Env:     cloneStringMap(command.Env),
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].Service != plans[j].Service {
			return plans[i].Service < plans[j].Service
		}
		return plans[i].Path < plans[j].Path
	})
	return plans, nil
}

func readDBSeedCommandInputs(appRoot string, rawInputs []string) ([]string, [][]byte, error) {
	type inputFile struct {
		path string
		data []byte
	}
	files := make([]inputFile, 0, len(rawInputs))
	seen := make(map[string]struct{}, len(rawInputs))
	for _, raw := range rawInputs {
		path, absolute, err := normalizeDBSeedWorkspacePath(appRoot, raw, false)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := seen[path]; exists {
			return nil, nil, fmt.Errorf("duplicate input %q", path)
		}
		seen[path] = struct{}{}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		files = append(files, inputFile{path: path, data: data})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	paths := make([]string, len(files))
	data := make([][]byte, len(files))
	for index, file := range files {
		paths[index] = file.path
		data[index] = file.data
	}
	return paths, data, nil
}

func normalizeDBSeedCommandCWD(appRoot, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	path, _, err := normalizeDBSeedWorkspacePath(appRoot, raw, true)
	return path, err
}

func normalizeDBSeedWorkspacePath(appRoot, raw string, wantDirectory bool) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(raw) {
		return "", "", fmt.Errorf("path %q must be workspace-relative", raw)
	}
	root, err := filepath.Abs(appRoot)
	if err != nil {
		return "", "", err
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.Clean(filepath.FromSlash(raw))))
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q escapes the app workspace", raw)
	}
	if err := rejectDBSeedSymlinkPath(root, relative); err != nil {
		return "", "", err
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", "", fmt.Errorf("inspect %s: %w", filepath.ToSlash(relative), err)
	}
	if wantDirectory {
		if !info.IsDir() {
			return "", "", fmt.Errorf("%s is not a directory", filepath.ToSlash(relative))
		}
	} else if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s is not a regular file", filepath.ToSlash(relative))
	}
	return filepath.ToSlash(relative), candidate, nil
}

func rejectDBSeedSymlinkPath(root, relative string) error {
	current := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", filepath.ToSlash(relative), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink: %s", filepath.ToSlash(relative))
		}
	}
	return nil
}

func writeDBSeedCommandHashPart(writer io.Writer, label string, data []byte) {
	_, _ = fmt.Fprintf(writer, "%d:%s:%d\n", len(label), label, len(data))
	_, _ = writer.Write(data)
	_, _ = io.WriteString(writer, "\n")
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func runDBSeedCommandPlan(ctx context.Context, appRoot, dsn string, baseEnv []string, plan dbSeedPlan, hooks dbSeedHooks) (string, error) {
	program, args := shellInvocation(plan.Command)
	env := overlayEnv(baseEnv, plan.Env)
	env = overlayEnv(env, map[string]string{appDatabaseURLEnv: dsn})
	output, err := hooks.runCommand(ctx, lifecycleExecRequest{
		Dir:     resolveLifecycleCWD(appRoot, plan.CWD),
		Env:     env,
		Program: program,
		Args:    args,
	})
	if err != nil {
		return strings.TrimSpace(output.Stdout), err
	}
	return strings.TrimSpace(output.Stdout), nil
}
