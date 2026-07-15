package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/validation"
)

const (
	validationInspectKind       = "scenery.inspect.validation"
	validationListKind          = "scenery.validation.list"
	validationInspectDetailKind = "scenery.validation.inspect"
	validationGraphKind         = "scenery.validation.graph"
	validationPlanKind          = "scenery.validation.plan"
	validationResultKind        = "scenery.validation.result"
)

type validateOptions struct {
	Action  string
	Profile string
	AppRoot string
	JSON    bool
	Write   bool
	DryRun  bool
	Base    string
}

type validationProfileRecord struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Cost        string   `json:"cost,omitempty"`
	Paths       []string `json:"paths"`
	Steps       []string `json:"steps"`
	EnvKeys     []string `json:"env_keys,omitempty"`
	Artifacts   []string `json:"artifacts"`
	Default     bool     `json:"default,omitempty"`
	StepCount   int      `json:"step_count,omitempty"`
}

type inspectValidationResponse struct {
	cliPayloadIdentity
	App         inspectdata.AppRef        `json:"app"`
	Default     string                    `json:"default,omitempty"`
	Profiles    []validationProfileRecord `json:"profiles"`
	Diagnostics []validation.Diagnostic   `json:"diagnostics"`
}

type validationListResponse struct {
	cliPayloadIdentity
	App         inspectdata.AppRef        `json:"app"`
	Default     string                    `json:"default,omitempty"`
	Profiles    []validationProfileRecord `json:"profiles"`
	Diagnostics []validation.Diagnostic   `json:"diagnostics,omitempty"`
}

type validationInspectResponse struct {
	cliPayloadIdentity
	App         inspectdata.AppRef      `json:"app"`
	Profile     validationProfileRecord `json:"profile"`
	Resolved    []validation.PlanStep   `json:"resolved_steps"`
	Tasks       []taskListRecord        `json:"tasks,omitempty"`
	Diagnostics []validation.Diagnostic `json:"diagnostics,omitempty"`
	Source      string                  `json:"source"`
}

type validationGraphResponse struct {
	cliPayloadIdentity
	App         inspectdata.AppRef      `json:"app"`
	Profile     string                  `json:"profile"`
	Nodes       []validationGraphNode   `json:"nodes"`
	Edges       []validationGraphEdge   `json:"edges"`
	Diagnostics []validation.Diagnostic `json:"diagnostics,omitempty"`
}

type validationGraphNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
}

type validationGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type validationPlanResponse struct {
	cliPayloadIdentity
	OK          bool                    `json:"ok"`
	App         inspectdata.AppRef      `json:"app"`
	Profile     string                  `json:"profile"`
	Selection   validation.Selection    `json:"selection"`
	Steps       []validation.PlanStep   `json:"steps"`
	Diagnostics []validation.Diagnostic `json:"diagnostics,omitempty"`
}

type validationResultResponse struct {
	cliPayloadIdentity
	OK          bool                    `json:"ok"`
	GeneratedAt string                  `json:"generated_at"`
	App         inspectdata.AppRef      `json:"app"`
	Profile     string                  `json:"profile"`
	Selection   validation.Selection    `json:"selection"`
	Steps       []validationResultStep  `json:"steps"`
	Artifacts   []validation.Artifact   `json:"artifacts,omitempty"`
	Diagnostics []validation.Diagnostic `json:"diagnostics,omitempty"`
	NextActions []string                `json:"next_actions,omitempty"`
	Wrote       string                  `json:"wrote,omitempty"`
}

type validationResultStep struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Profile    string           `json:"profile,omitempty"`
	OK         bool             `json:"ok"`
	DurationMS int64            `json:"duration_ms"`
	Evidence   *harnessEvidence `json:"evidence,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type validationArtifactContext struct {
	Root    string
	Enabled bool
	RunID   string
}

func newValidationPlanner(appRoot string, cfg appcfg.Config) validation.Planner {
	return validation.Planner{
		AppRoot: appRoot,
		Config:  cfg,
		App:     taskAppRef(appRoot, cfg),
		ValidateTaskTarget: func(target string) error {
			_, err := taskTargetKind(target)
			return err
		},
	}
}

func validateCommand(args []string) error {
	return runSceneryValidate(context.Background(), os.Stdout, args)
}

func runSceneryValidate(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseValidateArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	switch opts.Action {
	case "list":
		resp := buildValidationListResponse(appRoot, cfg)
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		for _, profile := range resp.Profiles {
			mark := " "
			if profile.Default {
				mark = "*"
			}
			fmt.Fprintf(stdout, "%s %s\t%s\t%d steps\n", mark, profile.Name, profile.Cost, profile.StepCount)
		}
		return nil
	case "inspect":
		resp, err := buildValidationInspectResponse(appRoot, cfg, opts.Profile)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		fmt.Fprintf(stdout, "%s\n  cost: %s\n  steps: %s\n", resp.Profile.Name, resp.Profile.Cost, strings.Join(resp.Profile.Steps, ", "))
		return nil
	case "graph":
		resp, err := buildValidationGraphResponse(appRoot, cfg, opts.Profile)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		for _, edge := range resp.Edges {
			fmt.Fprintf(stdout, "%s -> %s\t%s\n", edge.From, edge.To, edge.Kind)
		}
		return nil
	case "run", "changed":
		planner := newValidationPlanner(appRoot, cfg)
		plan, err := planner.Plan(ctx, validation.PlanRequest{
			Profile: opts.Profile,
			Changed: opts.Action == "changed",
			Base:    opts.Base,
		})
		if err != nil {
			return err
		}
		if opts.DryRun {
			resp := validationPlanResponse{
				cliPayloadIdentity: newCLIPayloadIdentity(validationPlanKind),
				OK:                 len(plan.Diagnostics) == 0,
				App:                plan.App,
				Profile:            plan.Profile,
				Selection:          plan.Selection,
				Steps:              plan.Steps,
				Diagnostics:        plan.Diagnostics,
			}
			if opts.JSON {
				return writeInspectJSON(stdout, resp)
			}
			for _, step := range resp.Steps {
				fmt.Fprintf(stdout, "%s\t%s\n", step.Kind, step.Name)
			}
			return nil
		}
		result := executeValidationPlan(ctx, appRoot, cfg, plan, opts)
		if opts.Write {
			if err := writeValidationResult(appRoot, &result); err != nil {
				return err
			}
		}
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			if !result.OK {
				return &silentCLIError{err: fmt.Errorf("scenery validate failed")}
			}
			return nil
		}
		if err := writeValidationText(stdout, result); err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("scenery validate failed")
		}
		return nil
	default:
		return fmt.Errorf("unknown validate action %q", opts.Action)
	}
}

func parseValidateArgs(args []string) (validateOptions, error) {
	opts := validateOptions{Action: "run", Base: "origin/main"}
	flags := newCLIFlagSet("validate")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.Write, "write", false, "")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "")
	flags.StringVar(&opts.Base, "base", opts.Base, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return validateOptions{}, err
	}
	if len(positionals) > 0 {
		switch positionals[0] {
		case "list", "inspect", "graph", "changed":
			opts.Action = positionals[0]
			positionals = positionals[1:]
		}
	}
	if opts.Action == "inspect" {
		if len(positionals) == 0 {
			return validateOptions{}, fmt.Errorf("missing validation profile")
		}
		opts.Profile = positionals[0]
		positionals = positionals[1:]
	} else if opts.Action == "graph" {
		if len(positionals) > 0 {
			opts.Profile = positionals[0]
			positionals = positionals[1:]
		}
	} else if opts.Action == "run" {
		if len(positionals) > 0 {
			opts.Profile = positionals[0]
			positionals = positionals[1:]
		}
	}
	if len(positionals) > 0 {
		return validateOptions{}, fmt.Errorf("unknown argument %q", positionals[0])
	}
	if cliFlagSet(flags, "base") && opts.Action != "changed" {
		return validateOptions{}, fmt.Errorf("--base is only supported for validate changed")
	}
	opts.Base = strings.TrimSpace(opts.Base)
	if opts.Base == "" {
		return validateOptions{}, fmt.Errorf("--base must not be empty")
	}
	if (opts.Action == "graph" || opts.Action == "list" || opts.Action == "inspect") && opts.Write {
		return validateOptions{}, fmt.Errorf("--write is only supported when running validation")
	}
	return opts, nil
}

func buildInspectValidationResponse(appRoot string, cfg appcfg.Config) inspectValidationResponse {
	return inspectValidationResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(validationInspectKind),
		App:                taskAppRef(appRoot, cfg),
		Default:            cfg.Validation.Default,
		Profiles:           validationProfileRecords(cfg),
		Diagnostics:        nonNilValidationDiagnostics(newValidationPlanner(appRoot, cfg).ValidateConfig()),
	}
}

func buildValidationListResponse(appRoot string, cfg appcfg.Config) validationListResponse {
	return validationListResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(validationListKind),
		App:                taskAppRef(appRoot, cfg),
		Default:            cfg.Validation.Default,
		Profiles:           validationProfileRecords(cfg),
		Diagnostics:        newValidationPlanner(appRoot, cfg).ValidateConfig(),
	}
}

func buildValidationInspectResponse(appRoot string, cfg appcfg.Config, profile string) (validationInspectResponse, error) {
	planner := newValidationPlanner(appRoot, cfg)
	profile = planner.ResolveProfileName(profile)
	rec, ok := validationProfileRecordFor(cfg, profile)
	if !ok {
		return validationInspectResponse{}, fmt.Errorf("validation profile %q is not configured", profile)
	}
	plan, _ := planner.NamedPlan(profile, validation.Selection{Mode: "explicit", Requested: []string{profile}})
	tasks := referencedValidationTasks(appRoot, cfg, plan.Steps)
	return validationInspectResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(validationInspectDetailKind),
		App:                taskAppRef(appRoot, cfg),
		Profile:            rec,
		Resolved:           plan.Steps,
		Tasks:              tasks,
		Diagnostics:        plan.Diagnostics,
		Source:             cfg.SourcePath(appRoot),
	}, nil
}

func buildValidationGraphResponse(appRoot string, cfg appcfg.Config, profile string) (validationGraphResponse, error) {
	planner := newValidationPlanner(appRoot, cfg)
	profile = planner.ResolveProfileName(profile)
	if _, ok := cfg.Validation.Profiles[profile]; !ok {
		return validationGraphResponse{}, fmt.Errorf("validation profile %q is not configured", profile)
	}
	resp := validationGraphResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(validationGraphKind),
		App:                taskAppRef(appRoot, cfg),
		Profile:            profile,
		Nodes:              []validationGraphNode{},
		Edges:              []validationGraphEdge{},
	}
	seen := map[string]bool{}
	configRel := cfg.SourceRelPath(appRoot)
	addNode := func(node validationGraphNode) {
		if node.ID == "" || seen[node.ID] {
			return
		}
		seen[node.ID] = true
		resp.Nodes = append(resp.Nodes, node)
	}
	var walk func(name string, stack []string)
	walk = func(name string, stack []string) {
		id := "profile:" + name
		addNode(validationGraphNode{ID: id, Name: name, Kind: "profile", Source: configRel})
		for _, active := range stack {
			if active == name {
				resp.Diagnostics = append(resp.Diagnostics, validationDiagnostic("validation", "error", "profile cycle detected: "+strings.Join(append(stack, name), " -> ")))
				return
			}
		}
		prof, ok := cfg.Validation.Profiles[name]
		if !ok {
			resp.Diagnostics = append(resp.Diagnostics, validationDiagnostic("validation", "error", "unknown validation profile "+name))
			return
		}
		for _, step := range prof.Steps {
			ref := validation.ParseStepRef(step)
			if ref.Name == "" {
				continue
			}
			childID := ref.Kind + ":" + ref.Name
			if ref.Kind == "builtin" {
				childID = "builtin:" + ref.Name
			}
			addNode(validationGraphNode{ID: childID, Name: ref.Name, Kind: ref.Kind, Source: validationStepSource(ref, configRel)})
			resp.Edges = append(resp.Edges, validationGraphEdge{From: id, To: childID, Kind: ref.Kind})
			if ref.Kind == "profile" {
				walk(ref.Name, append(stack, name))
			}
		}
	}
	walk(profile, nil)
	sort.Slice(resp.Nodes, func(i, j int) bool { return resp.Nodes[i].ID < resp.Nodes[j].ID })
	sort.Slice(resp.Edges, func(i, j int) bool {
		if resp.Edges[i].From == resp.Edges[j].From {
			return resp.Edges[i].To < resp.Edges[j].To
		}
		return resp.Edges[i].From < resp.Edges[j].From
	})
	resp.Diagnostics = append(resp.Diagnostics, planner.ValidateConfig()...)
	return resp, nil
}

func validationStepSource(ref validation.StepRef, configRel string) string {
	if ref.Kind == "builtin" {
		return "scenery"
	}
	return configRel
}

func validationProfileRecords(cfg appcfg.Config) []validationProfileRecord {
	names := make([]string, 0, len(cfg.Validation.Profiles))
	for name := range cfg.Validation.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]validationProfileRecord, 0, len(names))
	for _, name := range names {
		rec, _ := validationProfileRecordFor(cfg, name)
		out = append(out, rec)
	}
	return out
}

func validationProfileRecordFor(cfg appcfg.Config, name string) (validationProfileRecord, bool) {
	prof, ok := cfg.Validation.Profiles[name]
	if !ok {
		return validationProfileRecord{}, false
	}
	return validationProfileRecord{
		Name:        name,
		Description: prof.Description,
		Cost:        prof.Cost,
		Paths:       nonNilStrings(prof.Paths),
		Steps:       nonNilStrings(prof.Steps),
		EnvKeys:     sortedMapKeys(prof.Env),
		Artifacts:   nonNilStrings(prof.Artifacts),
		Default:     name == cfg.Validation.Default || cfg.Validation.Default == "" && name == "quick",
		StepCount:   len(prof.Steps),
	}, true
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func nonNilValidationDiagnostics(values []validation.Diagnostic) []validation.Diagnostic {
	if values == nil {
		return []validation.Diagnostic{}
	}
	return values
}

func validationDiagnostic(stage, severity, message string) validation.Diagnostic {
	return validation.Diagnostic{Stage: stage, Severity: severity, Message: message}
}

func referencedValidationTasks(appRoot string, cfg appcfg.Config, steps []validation.PlanStep) []taskListRecord {
	var out []taskListRecord
	seen := map[string]bool{}
	for _, step := range steps {
		ref := validation.ParseStepRef(step.Name)
		if ref.Kind != "task" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		if _, err := taskTargetKind(ref.Name); err != nil {
			continue
		}
		target, err := parseScriptTarget(ref.Name)
		if err != nil {
			continue
		}
		candidate, _, err := resolveScriptCandidate(appRoot, target, "")
		if err == nil {
			out = append(out, codeTaskListRecord(candidate))
		}
	}
	return out
}

func executeValidationPlan(ctx context.Context, appRoot string, cfg appcfg.Config, plan validation.ResolvedPlan, opts validateOptions) validationResultResponse {
	artifactCtx := newValidationArtifactContext(appRoot, opts.Write)
	run := func(ctx context.Context, step validation.PlanStep, stdout, stderr io.Writer) error {
		return runValidationStepCommand(ctx, appRoot, cfg, step, stdout, stderr, opts.JSON)
	}
	writeArtifacts := func(stepName string, stdout, stderr []byte) ([]validation.OutputArtifact, []validation.Diagnostic) {
		return writeValidationOutputArtifacts(artifactCtx, stepName, stdout, stderr)
	}
	return validationResultResponseFrom(validation.ExecutePlan(ctx, plan, run, writeArtifacts))
}

func validationResultResponseFrom(result validation.Result) validationResultResponse {
	resp := validationResultResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(validationResultKind),
		OK:                 result.OK,
		GeneratedAt:        result.GeneratedAt,
		App:                result.App,
		Profile:            result.Profile,
		Selection:          result.Selection,
		Steps:              make([]validationResultStep, 0, len(result.Steps)),
		Artifacts:          result.Artifacts,
		Diagnostics:        result.Diagnostics,
		NextActions:        result.NextActions,
	}
	for _, step := range result.Steps {
		resp.Steps = append(resp.Steps, validationResultStepFrom(step))
	}
	return resp
}

func validationResultStepFrom(step validation.StepResult) validationResultStep {
	evidence := newHarnessEvidence(step.Command, step.CWD, step.Started)
	var artifacts []harnessEvidenceArtifact
	for _, artifact := range step.Artifacts {
		artifacts = append(artifacts, harnessEvidenceArtifact{Name: artifact.Name, Path: artifact.Path})
	}
	finalizeHarnessEvidence(&evidence, step.Duration, step.OK, step.Stdout, step.Stderr, exitCodeFromError(step.Err), artifacts)
	return validationResultStep{
		ID:         step.ID,
		Name:       step.Name,
		Kind:       step.Kind,
		Profile:    step.Profile,
		OK:         step.OK,
		DurationMS: step.Duration.Milliseconds(),
		Evidence:   &evidence,
		Error:      step.Error,
	}
}

func runValidationStepCommand(ctx context.Context, appRoot string, cfg appcfg.Config, step validation.PlanStep, stdout, stderr io.Writer, capture bool) error {
	ref := validation.ParseStepRef(step.Name)
	if !capture {
		stdout = os.Stdout
		stderr = os.Stderr
	}
	switch ref.Kind {
	case "task":
		return runValidationTask(ctx, appRoot, cfg, ref.Name, nil, stdout, stderr, step.Env)
	case "builtin":
		switch ref.Name {
		case "harness", "harness:core":
			return runSceneryHarness(ctx, stdout, []string{"--app-root", appRoot, "-o", "json"})
		case "harness:ui":
			return runSceneryHarnessUI(ctx, stdout, []string{"--app-root", appRoot, "-o", "json"})
		case "check":
			return runSceneryCheck(ctx, stdout, []string{"--app-root", appRoot, "-o", "json"})
		case "test", "test:go":
			return runSceneryTestOutput(ctx, []string{"--app-root", appRoot}, stdout)
		case "generate":
			return runGenerate(ctx, stdout, []string{"--app-root", appRoot})
		case "generate:sqlc":
			return runGenerate(ctx, stdout, []string{"sqlc", "--app-root", appRoot})
		case "db:apply":
			if capture {
				return validation.RunWithCapturedProcessOutput(stdout, stderr, func() error {
					return dbApplyCommand([]string{"--app-root", appRoot})
				})
			}
			return dbApplyCommand([]string{"--app-root", appRoot})
		case "db:seed":
			if capture {
				return validation.RunWithCapturedProcessOutput(stdout, stderr, func() error {
					return dbSeedCommand([]string{"--app-root", appRoot})
				})
			}
			return dbSeedCommand([]string{"--app-root", appRoot})
		case "db:setup":
			if capture {
				return validation.RunWithCapturedProcessOutput(stdout, stderr, func() error {
					return dbSetupCommand([]string{"--app-root", appRoot})
				})
			}
			return dbSetupCommand([]string{"--app-root", appRoot})
		}
	}
	return fmt.Errorf("unsupported validation step %q", step.Name)
}

func runValidationTask(ctx context.Context, appRoot string, cfg appcfg.Config, target string, stack []string, stdout, stderr io.Writer, envOverlay map[string]string) error {
	if _, err := taskTargetKind(target); err != nil {
		return err
	}
	return runSceneryScript(ctx, scriptOptions{
		AppRoot:    appRoot,
		EnvOverlay: envOverlay,
		Target:     target,
		Stdout:     stdout,
		Stderr:     stderr,
		Stdin:      os.Stdin,
	})
}

func newValidationArtifactContext(root string, enabled bool) validationArtifactContext {
	return validationArtifactContext{
		Root:    root,
		Enabled: enabled,
		RunID:   time.Now().UTC().Format("20060102T150405.000000000Z"),
	}
}

func writeValidationOutputArtifacts(ctx validationArtifactContext, stepName string, stdout, stderr []byte) ([]validation.OutputArtifact, []validation.Diagnostic) {
	if !ctx.Enabled {
		return nil, nil
	}
	var artifacts []validation.OutputArtifact
	var diags []validation.Diagnostic
	write := func(kind string, data []byte) {
		if len(data) == 0 {
			return
		}
		name := sanitizeHarnessArtifactName(stepName + " " + kind)
		filename := sanitizeHarnessArtifactFilename(name + "." + kind + ".txt")
		rel := filepath.ToSlash(filepath.Join(".scenery", "harness", "validation", "artifacts", ctx.RunID, filename))
		abs := filepath.Join(ctx.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			diags = append(diags, validation.Diagnostic(formatArtifactWriteError(stepName, err)))
			return
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			diags = append(diags, validation.Diagnostic(formatArtifactWriteError(stepName, err)))
			return
		}
		artifacts = append(artifacts, validation.OutputArtifact{Name: name, Path: rel})
	}
	write("stdout", stdout)
	write("stderr", stderr)
	return artifacts, diags
}

func writeValidationResult(appRoot string, result *validationResultResponse) error {
	dir := filepath.Join(appRoot, ".scenery", "harness", "validation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	result.Wrote = filepath.Join(appRoot, ".scenery", "harness", "validation", "latest.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(result.Wrote, data, 0o644); err != nil {
		return err
	}
	if result.Profile != "" {
		profileLatest := filepath.Join(dir, sanitizeHarnessArtifactFilename(result.Profile+"-latest.json"))
		if err := os.WriteFile(profileLatest, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeValidationText(stdout io.Writer, result validationResultResponse) error {
	fmt.Fprintf(stdout, "profile %s\n", result.Profile)
	for _, step := range result.Steps {
		status := "ok"
		if !step.OK {
			status = "fail"
		}
		fmt.Fprintf(stdout, "  %-4s %-28s %.1fs\n", status, step.Name, float64(step.DurationMS)/1000)
	}
	if result.OK {
		fmt.Fprintln(stdout, "\nvalidation ok")
		return nil
	}
	if len(result.Steps) > 0 {
		failed := result.Steps[len(result.Steps)-1]
		fmt.Fprintf(stdout, "\nfailed: %s\n", failed.Name)
		if failed.Evidence != nil && failed.Evidence.ReproCommand != "" {
			fmt.Fprintf(stdout, "repro: %s\n", failed.Evidence.ReproCommand)
		}
		for _, artifact := range failed.Evidence.Artifacts {
			fmt.Fprintf(stdout, "artifact: %s\n", artifact.Path)
		}
	}
	return nil
}
