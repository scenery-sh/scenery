package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
)

type taskOptions struct {
	Action  string
	Target  string
	AppRoot string
	Env     string
	Lang    string
	JSON    bool
	Args    []string
}

type taskGraphResponse struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Tasks         []taskRecord       `json:"tasks,omitempty"`
	Nodes         []taskGraphNode    `json:"nodes"`
	Edges         []taskGraphEdge    `json:"edges"`
}

type taskRecord struct {
	Name    string   `json:"name"`
	CWD     string   `json:"cwd,omitempty"`
	Run     string   `json:"run,omitempty"`
	Steps   []string `json:"steps,omitempty"`
	EnvKeys []string `json:"env_keys,omitempty"`
}

type taskListResponse struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Tasks         []taskListRecord   `json:"tasks"`
}

type taskListRecord struct {
	Target       string   `json:"target"`
	Kind         string   `json:"kind"`
	Source       string   `json:"source"`
	Name         string   `json:"name,omitempty"`
	CWD          string   `json:"cwd,omitempty"`
	Run          string   `json:"run,omitempty"`
	Steps        []string `json:"steps,omitempty"`
	EnvKeys      []string `json:"env_keys,omitempty"`
	Language     string   `json:"language,omitempty"`
	Layout       string   `json:"layout,omitempty"`
	ArgsAccepted bool     `json:"args_accepted"`
}

type taskInspectResponse struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Task          taskListRecord     `json:"task"`
	Searched      []string           `json:"searched,omitempty"`
}

type taskGraphNode struct {
	Target       string `json:"target"`
	Kind         string `json:"kind"`
	Source       string `json:"source,omitempty"`
	Language     string `json:"language,omitempty"`
	ArgsAccepted bool   `json:"args_accepted,omitempty"`
}

type taskGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

const (
	taskKindConfigured = "configured"
	taskKindCode       = "code"
	taskKindBuiltin    = "builtin"
)

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
	if err := validateConfiguredTaskNames(cfg); err != nil {
		return err
	}
	switch opts.Action {
	case "list":
		list, err := buildTaskList(appRoot, cfg)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, list)
		}
		for _, task := range list.Tasks {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", task.Target, task.Kind, task.Source)
		}
		return nil
	case "inspect":
		resp, err := buildTaskInspect(appRoot, cfg, opts)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		task := resp.Task
		fmt.Fprintf(stdout, "%s\n  kind: %s\n  source: %s\n", task.Target, task.Kind, task.Source)
		if task.Language != "" {
			fmt.Fprintf(stdout, "  language: %s\n", task.Language)
		}
		if task.Run != "" {
			fmt.Fprintf(stdout, "  run: %s\n", task.Run)
		}
		if len(task.Steps) > 0 {
			fmt.Fprintf(stdout, "  steps: %s\n", strings.Join(task.Steps, ", "))
		}
		return nil
	case "graph":
		if !opts.JSON {
			return fmt.Errorf("scenery task graph requires --json")
		}
		graph, err := buildTaskGraph(appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, graph)
	case "run":
		return runTaskTarget(ctx, appRoot, cfg, opts)
	default:
		return fmt.Errorf("unknown task command %q", opts.Action)
	}
}

func parseTaskArgs(args []string) (taskOptions, error) {
	if len(args) == 0 {
		return taskOptions{}, fmt.Errorf("usage: scenery task list|inspect|run|graph [--app-root <path>] [--json]")
	}
	opts := taskOptions{Action: args[0]}
	args = args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			if opts.Action != "run" {
				return taskOptions{}, fmt.Errorf("-- is only supported for task run")
			}
			opts.Args = append([]string(nil), args[i+1:]...)
			i = len(args)
		case arg == "--app-root":
			i++
			if i >= len(args) {
				return taskOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case strings.HasPrefix(arg, "--app-root="):
			opts.AppRoot = strings.TrimPrefix(arg, "--app-root=")
		case arg == "--env":
			if opts.Action != "run" {
				return taskOptions{}, fmt.Errorf("--env is only supported for task run")
			}
			i++
			if i >= len(args) {
				return taskOptions{}, fmt.Errorf("missing value for --env")
			}
			opts.Env = strings.TrimSpace(args[i])
			if opts.Env == "" {
				return taskOptions{}, fmt.Errorf("--env must not be empty")
			}
		case strings.HasPrefix(arg, "--env="):
			if opts.Action != "run" {
				return taskOptions{}, fmt.Errorf("--env is only supported for task run")
			}
			opts.Env = strings.TrimSpace(strings.TrimPrefix(arg, "--env="))
			if opts.Env == "" {
				return taskOptions{}, fmt.Errorf("--env must not be empty")
			}
		case arg == "--lang":
			if opts.Action != "run" && opts.Action != "inspect" {
				return taskOptions{}, fmt.Errorf("--lang is only supported for task inspect and task run")
			}
			i++
			if i >= len(args) {
				return taskOptions{}, fmt.Errorf("missing value for --lang")
			}
			lang, err := normalizeScriptLang(args[i])
			if err != nil {
				return taskOptions{}, err
			}
			opts.Lang = lang
		case strings.HasPrefix(arg, "--lang="):
			if opts.Action != "run" && opts.Action != "inspect" {
				return taskOptions{}, fmt.Errorf("--lang is only supported for task inspect and task run")
			}
			lang, err := normalizeScriptLang(strings.TrimPrefix(arg, "--lang="))
			if err != nil {
				return taskOptions{}, err
			}
			opts.Lang = lang
		case arg == "--json":
			opts.JSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				return taskOptions{}, fmt.Errorf("unknown flag %q", arg)
			}
			if opts.Action != "run" && opts.Action != "inspect" {
				return taskOptions{}, fmt.Errorf("unexpected argument %q", arg)
			}
			if opts.Target != "" {
				return taskOptions{}, fmt.Errorf("unexpected argument %q; pass task arguments after --", arg)
			}
			opts.Target = arg
		}
	}
	switch opts.Action {
	case "list", "graph":
		if opts.Target != "" {
			return taskOptions{}, fmt.Errorf("unexpected task target %q", opts.Target)
		}
	case "inspect", "run":
		if opts.Target == "" {
			return taskOptions{}, fmt.Errorf("missing task target")
		}
		if _, err := taskTargetKind(opts.Target); err != nil {
			return taskOptions{}, err
		}
	default:
		return taskOptions{}, fmt.Errorf("unknown task command %q", opts.Action)
	}
	return opts, nil
}

func buildTaskList(appRoot string, cfg appcfg.Config) (taskListResponse, error) {
	resp := taskListResponse{
		SchemaVersion: "scenery.task.list.v1",
		App:           taskAppRef(appRoot, cfg),
		Tasks:         []taskListRecord{},
	}
	names := make([]string, 0, len(cfg.Tasks))
	for name := range cfg.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		task := cfg.Tasks[name]
		resp.Tasks = append(resp.Tasks, configuredTaskListRecord(appRoot, name, task))
	}
	codeTasks, err := listScriptCandidates(appRoot)
	if err != nil {
		return taskListResponse{}, err
	}
	for _, task := range codeTasks {
		resp.Tasks = append(resp.Tasks, codeTaskListRecord(task))
	}
	return resp, nil
}

func buildTaskInspect(appRoot string, cfg appcfg.Config, opts taskOptions) (taskInspectResponse, error) {
	kind, err := taskTargetKind(opts.Target)
	if err != nil {
		return taskInspectResponse{}, err
	}
	resp := taskInspectResponse{
		SchemaVersion: "scenery.task.inspect.v1",
		App:           taskAppRef(appRoot, cfg),
	}
	switch kind {
	case taskKindConfigured:
		task, ok := cfg.Tasks[opts.Target]
		if !ok {
			return taskInspectResponse{}, fmt.Errorf("configured task %q is not configured", opts.Target)
		}
		resp.Task = configuredTaskListRecord(appRoot, opts.Target, task)
	case taskKindCode:
		target, err := parseScriptTarget(opts.Target)
		if err != nil {
			return taskInspectResponse{}, err
		}
		candidate, searched, err := resolveScriptCandidate(appRoot, target, opts.Lang)
		if err != nil {
			return taskInspectResponse{}, err
		}
		resp.Task = codeTaskListRecord(candidate)
		for _, item := range searched {
			resp.Searched = append(resp.Searched, item.Path)
		}
	}
	return resp, nil
}

func buildTaskGraph(appRoot string, cfg appcfg.Config) (taskGraphResponse, error) {
	names := make([]string, 0, len(cfg.Tasks))
	for name := range cfg.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	resp := taskGraphResponse{
		SchemaVersion: "scenery.task.graph.v1",
		App:           taskAppRef(appRoot, cfg),
		Tasks:         []taskRecord{},
		Nodes:         []taskGraphNode{},
		Edges:         []taskGraphEdge{},
	}
	seenNodes := map[string]bool{}
	addNode := func(node taskGraphNode) {
		if node.Target == "" || seenNodes[node.Kind+"\x00"+node.Target] {
			return
		}
		seenNodes[node.Kind+"\x00"+node.Target] = true
		resp.Nodes = append(resp.Nodes, node)
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
		addNode(taskGraphNode{Target: name, Kind: taskKindConfigured, Source: ".scenery.json"})
		for _, step := range task.Steps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			if strings.HasPrefix(step, "task:") {
				target := strings.TrimSpace(strings.TrimPrefix(step, "task:"))
				if target == "" {
					continue
				}
				kind, err := taskTargetKind(target)
				if err != nil {
					kind = "invalid"
				}
				addNode(taskGraphNode{Target: target, Kind: kind, ArgsAccepted: kind == taskKindCode})
				resp.Edges = append(resp.Edges, taskGraphEdge{From: name, To: target, Kind: "task"})
				continue
			}
			addNode(taskGraphNode{Target: step, Kind: taskKindBuiltin, Source: "scenery"})
			resp.Edges = append(resp.Edges, taskGraphEdge{From: name, To: step, Kind: taskKindBuiltin})
		}
	}
	codeTasks, err := listScriptCandidates(appRoot)
	if err != nil {
		return taskGraphResponse{}, err
	}
	for _, task := range codeTasks {
		addNode(taskGraphNode{
			Target:       scriptTargetString(task.Target),
			Kind:         taskKindCode,
			Source:       task.Path,
			Language:     task.Lang,
			ArgsAccepted: true,
		})
	}
	return resp, nil
}

func taskAppRef(appRoot string, cfg appcfg.Config) inspectdata.AppRef {
	return inspectdata.AppRef{
		Name:       cfg.Name,
		ID:         cfg.ID,
		Root:       appRoot,
		ConfigPath: filepath.Join(appRoot, ".scenery.json"),
	}
}

func configuredTaskListRecord(appRoot, name string, task appcfg.TaskConfig) taskListRecord {
	return taskListRecord{
		Target:       name,
		Kind:         taskKindConfigured,
		Source:       ".scenery.json",
		Name:         name,
		CWD:          task.CWD,
		Run:          task.Run,
		Steps:        append([]string(nil), task.Steps...),
		EnvKeys:      sortedMapKeys(task.Env),
		ArgsAccepted: false,
	}
}

func codeTaskListRecord(task scriptCandidate) taskListRecord {
	return taskListRecord{
		Target:       scriptTargetString(task.Target),
		Kind:         taskKindCode,
		Source:       task.Path,
		Name:         task.Target.Name,
		Language:     task.Lang,
		Layout:       task.Layout,
		ArgsAccepted: true,
	}
}

func validateConfiguredTaskNames(cfg appcfg.Config) error {
	for name := range cfg.Tasks {
		if strings.Contains(name, ":") {
			return fmt.Errorf("configured task %q is invalid: configured task names cannot contain ':'", name)
		}
	}
	return nil
}

func taskTargetKind(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("missing task target")
	}
	if strings.Contains(target, ":") {
		if strings.Count(target, ":") != 1 {
			return "", fmt.Errorf("invalid code task target %q; code-backed task targets must contain exactly one ':'", target)
		}
		if _, err := parseScriptTarget(target); err != nil {
			return "", err
		}
		return taskKindCode, nil
	}
	if !validScriptSegment(target) {
		return "", fmt.Errorf("invalid configured task target %q; configured task names must match [A-Za-z0-9_][A-Za-z0-9_-]*", target)
	}
	return taskKindConfigured, nil
}

func runTaskTarget(ctx context.Context, appRoot string, cfg appcfg.Config, opts taskOptions) error {
	kind, err := taskTargetKind(opts.Target)
	if err != nil {
		return err
	}
	switch kind {
	case taskKindConfigured:
		if opts.Env != "" {
			return fmt.Errorf("--env is only supported for code tasks")
		}
		if opts.Lang != "" {
			return fmt.Errorf("--lang is only supported for code tasks")
		}
		if len(opts.Args) > 0 {
			return fmt.Errorf("configured task %q does not accept arguments", opts.Target)
		}
		return runConfiguredTask(ctx, appRoot, cfg, opts.Target, nil)
	case taskKindCode:
		return runSceneryScript(ctx, scriptOptions{
			AppRoot: appRoot,
			Env:     opts.Env,
			Lang:    opts.Lang,
			Target:  opts.Target,
			Args:    append([]string(nil), opts.Args...),
		})
	default:
		return fmt.Errorf("unsupported task target kind %q", kind)
	}
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
		return runTaskShellCommand(ctx, appRoot, cfg, task)
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

func runTaskShellCommand(ctx context.Context, appRoot string, cfg appcfg.Config, task appcfg.TaskConfig) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	storageEnv, err := storageCapabilityEnv(cfg, nil, env, "")
	if err != nil {
		return err
	}
	env = envWithOverrides(env, storageEnv...)
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
		target := strings.TrimSpace(strings.TrimPrefix(step, "task:"))
		if target == "" {
			return fmt.Errorf("task step %q is missing a task target", step)
		}
		kind, err := taskTargetKind(target)
		if err != nil {
			return err
		}
		if kind == taskKindConfigured {
			return runConfiguredTask(ctx, appRoot, cfg, target, stack)
		}
		return runTaskTarget(ctx, appRoot, cfg, taskOptions{Action: "run", Target: target})
	case step == "check":
		return runSceneryCheck(ctx, os.Stdout, []string{"--app-root", appRoot})
	case step == "test":
		return runSceneryTest(ctx, []string{"--app-root", appRoot})
	case step == "test:go":
		return runSceneryTest(ctx, []string{"--app-root", appRoot})
	case step == "generate":
		return runGenerate(ctx, os.Stdout, []string{"--app-root", appRoot})
	case step == "generate:client":
		return runGenerate(ctx, os.Stdout, []string{"client", "--app-root", appRoot})
	case step == "generate:sqlc":
		return runGenerate(ctx, os.Stdout, []string{"sqlc", "--app-root", appRoot})
	case step == "db:apply":
		return dbApplyCommand([]string{"--app-root", appRoot})
	case step == "db:seed":
		return dbSeedCommand([]string{"--app-root", appRoot})
	case step == "db:setup":
		return dbSetupCommand([]string{"--app-root", appRoot})
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
