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
	Nodes         []taskGraphNode    `json:"nodes"`
	Edges         []taskGraphEdge    `json:"edges"`
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
	taskKindCode = "code"
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
			return fmt.Errorf("scenery task graph requires -o json")
		}
		graph, err := buildTaskGraph(appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, graph)
	case "run":
		return runTaskTarget(ctx, appRoot, opts)
	default:
		return fmt.Errorf("unknown task command %q", opts.Action)
	}
}

func parseTaskArgs(args []string) (taskOptions, error) {
	if len(args) == 0 {
		return taskOptions{}, fmt.Errorf("usage: scenery task list|inspect|run|graph [--app-root <path>] [-o json]")
	}
	opts := taskOptions{Action: args[0]}
	before, passthrough, hasPassthrough := splitCLIPassthrough(args[1:])
	if hasPassthrough && opts.Action != "run" {
		return taskOptions{}, fmt.Errorf("-- is only supported for task run")
	}
	flags := newCLIFlagSet("task " + opts.Action)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Env, "env", "", "")
	lang := ""
	flags.StringVar(&lang, "lang", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, before)
	if err != nil {
		return taskOptions{}, err
	}
	if cliFlagSet(flags, "env") && opts.Action != "run" {
		return taskOptions{}, fmt.Errorf("--env is only supported for task run")
	}
	opts.Env = strings.TrimSpace(opts.Env)
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return taskOptions{}, fmt.Errorf("--env must not be empty")
	}
	if cliFlagSet(flags, "lang") && opts.Action != "run" && opts.Action != "inspect" {
		return taskOptions{}, fmt.Errorf("--lang is only supported for task inspect and task run")
	}
	opts.Lang, err = normalizeScriptLang(lang)
	if err != nil {
		return taskOptions{}, err
	}
	if len(positionals) > 0 {
		opts.Target = positionals[0]
	}
	if len(positionals) > 1 {
		return taskOptions{}, fmt.Errorf("unexpected argument %q; pass task arguments after --", positionals[1])
	}
	opts.Args = append([]string(nil), passthrough...)
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
	resp := taskGraphResponse{
		SchemaVersion: "scenery.task.graph.v1",
		App:           taskAppRef(appRoot, cfg),
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
		ConfigPath: cfg.SourcePath(appRoot),
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

func taskTargetKind(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("missing task target")
	}
	if strings.Count(target, ":") != 1 {
		return "", fmt.Errorf("invalid code task target %q; code-backed task targets must contain exactly one ':'", target)
	}
	if _, err := parseScriptTarget(target); err != nil {
		return "", err
	}
	return taskKindCode, nil
}

func runTaskTarget(ctx context.Context, appRoot string, opts taskOptions) error {
	kind, err := taskTargetKind(opts.Target)
	if err != nil {
		return err
	}
	switch kind {
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
