package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
)

type taskOptions struct {
	Action  string
	Name    string
	AppRoot string
	JSON    bool
}

type taskGraphResponse struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Tasks         []taskRecord       `json:"tasks"`
}

type taskRecord struct {
	Name    string   `json:"name"`
	CWD     string   `json:"cwd,omitempty"`
	Run     string   `json:"run,omitempty"`
	Steps   []string `json:"steps,omitempty"`
	EnvKeys []string `json:"env_keys,omitempty"`
}

func taskCommand(args []string) error {
	return runTaskCommand(context.Background(), os.Stdout, args)
}

func runTaskCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseTaskArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	switch opts.Action {
	case "list":
		graph := buildTaskGraph(appRoot, cfg)
		if opts.JSON {
			return writeInspectJSON(stdout, graph)
		}
		for _, task := range graph.Tasks {
			fmt.Fprintln(stdout, task.Name)
		}
		return nil
	case "graph":
		if !opts.JSON {
			return fmt.Errorf("onlava task graph requires --json")
		}
		return writeInspectJSON(stdout, buildTaskGraph(appRoot, cfg))
	case "run":
		return runConfiguredTask(ctx, appRoot, cfg, opts.Name, nil)
	default:
		return fmt.Errorf("unknown task command %q", opts.Action)
	}
}

func parseTaskArgs(args []string) (taskOptions, error) {
	if len(args) == 0 {
		return taskOptions{}, fmt.Errorf("usage: onlava task list|run|graph [--app-root <path>] [--json]")
	}
	opts := taskOptions{Action: args[0]}
	args = args[1:]
	if opts.Action == "run" {
		if len(args) == 0 {
			return taskOptions{}, fmt.Errorf("missing task name")
		}
		opts.Name = args[0]
		args = args[1:]
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--app-root":
			i++
			if i >= len(args) {
				return taskOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case strings.HasPrefix(arg, "--app-root="):
			opts.AppRoot = strings.TrimPrefix(arg, "--app-root=")
		case arg == "--json":
			opts.JSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				return taskOptions{}, fmt.Errorf("unknown flag %q", arg)
			}
			return taskOptions{}, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	switch opts.Action {
	case "list", "run", "graph":
	default:
		return taskOptions{}, fmt.Errorf("unknown task command %q", opts.Action)
	}
	return opts, nil
}

func buildTaskGraph(appRoot string, cfg appcfg.Config) taskGraphResponse {
	names := make([]string, 0, len(cfg.Tasks))
	for name := range cfg.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	resp := taskGraphResponse{
		SchemaVersion: "onlava.task.graph.v1",
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".onlava.json"),
		},
	}
	for _, name := range names {
		task := cfg.Tasks[name]
		resp.Tasks = append(resp.Tasks, taskRecord{
			Name:    name,
			CWD:     task.CWD,
			Run:     task.Run,
			Steps:   append([]string(nil), task.Steps...),
			EnvKeys: sortedMapKeys(task.Env),
		})
	}
	return resp
}

func runConfiguredTask(ctx context.Context, appRoot string, cfg appcfg.Config, name string, stack []string) error {
	task, ok := cfg.Tasks[name]
	if !ok {
		return fmt.Errorf("task %q is not configured", name)
	}
	for _, active := range stack {
		if active == name {
			return fmt.Errorf("task cycle detected: %s -> %s", strings.Join(stack, " -> "), name)
		}
	}
	stack = append(stack, name)
	if strings.TrimSpace(task.Run) != "" && len(task.Steps) > 0 {
		return fmt.Errorf("task %q cannot define both run and steps", name)
	}
	if strings.TrimSpace(task.Run) != "" {
		return runTaskShellCommand(ctx, appRoot, task)
	}
	if len(task.Steps) == 0 {
		return fmt.Errorf("task %q has no run command or steps", name)
	}
	for _, step := range task.Steps {
		if err := runTaskStep(ctx, appRoot, cfg, step, stack); err != nil {
			return err
		}
	}
	return nil
}

func runTaskShellCommand(ctx context.Context, appRoot string, task appcfg.TaskConfig) error {
	env, err := appEnvWithDotEnv(os.Environ(), appRoot)
	if err != nil {
		return err
	}
	program, args := shellInvocation(task.Run)
	return runLifecycleExec(ctx, lifecycleExecRequest{
		Dir:     resolveLifecycleCWD(appRoot, task.CWD),
		Env:     overlayEnv(env, task.Env),
		Program: program,
		Args:    args,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
}

func runTaskStep(ctx context.Context, appRoot string, cfg appcfg.Config, step string, stack []string) error {
	step = strings.TrimSpace(step)
	switch {
	case strings.HasPrefix(step, "task:"):
		name := strings.TrimSpace(strings.TrimPrefix(step, "task:"))
		if name == "" {
			return fmt.Errorf("task step %q is missing a task name", step)
		}
		return runConfiguredTask(ctx, appRoot, cfg, name, stack)
	case step == "check":
		return runOnlavaCheck(ctx, os.Stdout, []string{"--app-root", appRoot})
	case step == "test:go":
		return runOnlavaTest(ctx, []string{"--app-root", appRoot})
	case step == "generate":
		return runGenerate(ctx, os.Stdout, []string{"--app-root", appRoot})
	case step == "generate:client":
		return runGenerate(ctx, os.Stdout, []string{"client", "--app-root", appRoot})
	case step == "generate:sqlc":
		return runGenerate(ctx, os.Stdout, []string{"sqlc", "--app-root", appRoot})
	case step == "db:sync":
		return dbSyncCommand([]string{"--app-root", appRoot})
	default:
		return fmt.Errorf("unknown task step %q", step)
	}
}

func resolveLifecycleCWD(root, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return root
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Join(root, cwd)
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
