package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/envpolicy"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
)

const (
	validationInspectSchema       = "onlava.inspect.validation.v1"
	validationListSchema          = "onlava.validation.list.v1"
	validationInspectDetailSchema = "onlava.validation.inspect.v1"
	validationGraphSchema         = "onlava.validation.graph.v1"
	validationPlanSchema          = "onlava.validation.plan.v1"
	validationResultSchema        = "onlava.validation.result.v1"
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
	SchemaVersion string                    `json:"schema_version"`
	App           inspectdata.AppRef        `json:"app"`
	Default       string                    `json:"default,omitempty"`
	Profiles      []validationProfileRecord `json:"profiles"`
	Diagnostics   []checkDiagnostic         `json:"diagnostics"`
}

type validationListResponse struct {
	SchemaVersion string                    `json:"schema_version"`
	App           inspectdata.AppRef        `json:"app"`
	Default       string                    `json:"default,omitempty"`
	Profiles      []validationProfileRecord `json:"profiles"`
	Diagnostics   []checkDiagnostic         `json:"diagnostics,omitempty"`
}

type validationInspectResponse struct {
	SchemaVersion string                  `json:"schema_version"`
	App           inspectdata.AppRef      `json:"app"`
	Profile       validationProfileRecord `json:"profile"`
	Resolved      []validationPlanStep    `json:"resolved_steps"`
	Tasks         []taskListRecord        `json:"tasks,omitempty"`
	Diagnostics   []checkDiagnostic       `json:"diagnostics,omitempty"`
	Source        string                  `json:"source"`
}

type validationGraphResponse struct {
	SchemaVersion string                `json:"schema_version"`
	App           inspectdata.AppRef    `json:"app"`
	Profile       string                `json:"profile"`
	Nodes         []validationGraphNode `json:"nodes"`
	Edges         []validationGraphEdge `json:"edges"`
	Diagnostics   []checkDiagnostic     `json:"diagnostics,omitempty"`
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
	SchemaVersion string               `json:"schema_version"`
	OK            bool                 `json:"ok"`
	App           inspectdata.AppRef   `json:"app"`
	Profile       string               `json:"profile"`
	Selection     validationSelection  `json:"selection"`
	Steps         []validationPlanStep `json:"steps"`
	Diagnostics   []checkDiagnostic    `json:"diagnostics,omitempty"`
}

type validationSelection struct {
	Mode             string                   `json:"mode"`
	Requested        []string                 `json:"requested,omitempty"`
	Base             string                   `json:"base,omitempty"`
	ChangedFiles     []string                 `json:"changed_files,omitempty"`
	MatchedProfiles  []validationProfileMatch `json:"matched_profiles,omitempty"`
	ResolvedProfiles []string                 `json:"resolved_profiles"`
}

type validationProfileMatch struct {
	Profile      string   `json:"profile"`
	MatchedPaths []string `json:"matched_paths"`
	MatchedFiles []string `json:"matched_files"`
}

type validationPlanStep struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Profile  string            `json:"profile,omitempty"`
	Command  []string          `json:"command,omitempty"`
	CWD      string            `json:"cwd,omitempty"`
	Artifact []string          `json:"artifacts,omitempty"`
	Env      map[string]string `json:"-"`
}

type validationResultResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	OK            bool                   `json:"ok"`
	GeneratedAt   string                 `json:"generated_at"`
	App           inspectdata.AppRef     `json:"app"`
	Profile       string                 `json:"profile"`
	Selection     validationSelection    `json:"selection"`
	Steps         []validationResultStep `json:"steps"`
	Artifacts     []validationArtifact   `json:"artifacts,omitempty"`
	Diagnostics   []checkDiagnostic      `json:"diagnostics,omitempty"`
	NextActions   []string               `json:"next_actions,omitempty"`
	Wrote         string                 `json:"wrote,omitempty"`
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

type validationArtifact struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type validationResolvedPlan struct {
	App         inspectdata.AppRef
	Profile     string
	Selection   validationSelection
	Profiles    []string
	Steps       []validationPlanStep
	Diagnostics []checkDiagnostic
}

type validationArtifactContext struct {
	Root    string
	Enabled bool
	RunID   string
}

var collectValidationChangedFiles = func(ctx context.Context, appRoot, base string) ([]string, error) {
	rootCmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = appRoot
	rootOut, err := rootCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse --show-toplevel: %w: %s", err, strings.TrimSpace(string(rootOut)))
	}
	gitRoot := strings.TrimSpace(string(rootOut))
	if physicalGitRoot, err := filepath.EvalSymlinks(gitRoot); err == nil {
		gitRoot = physicalGitRoot
	}
	appRootForRel := appRoot
	if physicalAppRoot, err := filepath.EvalSymlinks(appRoot); err == nil {
		appRootForRel = physicalAppRoot
	}
	appRel, err := filepath.Rel(gitRoot, appRootForRel)
	if err != nil {
		return nil, err
	}
	appRel = filepath.ToSlash(appRel)
	args := []string{"diff", "--name-only", base + "...HEAD"}
	if appRel != "." && appRel != "" {
		args = []string{"diff", "--name-only", "--relative=" + appRel, base + "...HEAD", "--", appRel}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = gitRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(filepath.ToSlash(line))
		if line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files, nil
}

func validateCommand(args []string) error {
	return runOnlavaValidate(context.Background(), os.Stdout, args)
}

func runOnlavaValidate(ctx context.Context, stdout io.Writer, args []string) error {
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
		plan, err := buildValidationPlan(ctx, appRoot, cfg, opts)
		if err != nil {
			return err
		}
		if opts.DryRun {
			resp := validationPlanResponse{
				SchemaVersion: validationPlanSchema,
				OK:            len(plan.Diagnostics) == 0,
				App:           plan.App,
				Profile:       plan.Profile,
				Selection:     plan.Selection,
				Steps:         plan.Steps,
				Diagnostics:   plan.Diagnostics,
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
				return &silentCLIError{err: fmt.Errorf("onlava validate failed")}
			}
			return nil
		}
		if err := writeValidationText(stdout, result); err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("onlava validate failed")
		}
		return nil
	default:
		return fmt.Errorf("unknown validate action %q", opts.Action)
	}
}

func parseValidateArgs(args []string) (validateOptions, error) {
	opts := validateOptions{Action: "run", Base: "origin/main"}
	if len(args) > 0 {
		switch args[0] {
		case "list", "inspect", "graph", "changed":
			opts.Action = args[0]
			args = args[1:]
		}
	}
	if opts.Action == "inspect" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return validateOptions{}, fmt.Errorf("missing validation profile")
		}
		opts.Profile = args[0]
		args = args[1:]
	} else if opts.Action == "graph" {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			opts.Profile = args[0]
			args = args[1:]
		}
	} else if opts.Action == "run" {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			opts.Profile = args[0]
			args = args[1:]
		}
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--app-root":
			i++
			if i >= len(args) {
				return validateOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case strings.HasPrefix(args[i], "--app-root="):
			opts.AppRoot = strings.TrimPrefix(args[i], "--app-root=")
		case args[i] == "--json":
			opts.JSON = true
		case args[i] == "--write":
			opts.Write = true
		case args[i] == "--dry-run":
			opts.DryRun = true
		case args[i] == "--base":
			if opts.Action != "changed" {
				return validateOptions{}, fmt.Errorf("--base is only supported for validate changed")
			}
			i++
			if i >= len(args) {
				return validateOptions{}, fmt.Errorf("missing value for --base")
			}
			opts.Base = strings.TrimSpace(args[i])
			if opts.Base == "" {
				return validateOptions{}, fmt.Errorf("--base must not be empty")
			}
		case strings.HasPrefix(args[i], "--base="):
			if opts.Action != "changed" {
				return validateOptions{}, fmt.Errorf("--base is only supported for validate changed")
			}
			opts.Base = strings.TrimSpace(strings.TrimPrefix(args[i], "--base="))
			if opts.Base == "" {
				return validateOptions{}, fmt.Errorf("--base must not be empty")
			}
		default:
			return validateOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if (opts.Action == "graph" || opts.Action == "list" || opts.Action == "inspect") && opts.Write {
		return validateOptions{}, fmt.Errorf("--write is only supported when running validation")
	}
	return opts, nil
}

func buildInspectValidationResponse(appRoot string, cfg appcfg.Config) inspectValidationResponse {
	return inspectValidationResponse{
		SchemaVersion: validationInspectSchema,
		App:           taskAppRef(appRoot, cfg),
		Default:       cfg.Validation.Default,
		Profiles:      validationProfileRecords(cfg),
		Diagnostics:   nonNilValidationDiagnostics(validateValidationConfig(appRoot, cfg)),
	}
}

func buildValidationListResponse(appRoot string, cfg appcfg.Config) validationListResponse {
	return validationListResponse{
		SchemaVersion: validationListSchema,
		App:           taskAppRef(appRoot, cfg),
		Default:       cfg.Validation.Default,
		Profiles:      validationProfileRecords(cfg),
		Diagnostics:   validateValidationConfig(appRoot, cfg),
	}
}

func buildValidationInspectResponse(appRoot string, cfg appcfg.Config, profile string) (validationInspectResponse, error) {
	profile = resolveValidationProfileName(cfg, profile)
	rec, ok := validationProfileRecordFor(cfg, profile)
	if !ok {
		return validationInspectResponse{}, fmt.Errorf("validation profile %q is not configured", profile)
	}
	plan, _ := buildValidationNamedPlan(appRoot, cfg, profile, validationSelection{Mode: "explicit", Requested: []string{profile}})
	tasks := referencedValidationTasks(appRoot, cfg, plan.Steps)
	return validationInspectResponse{
		SchemaVersion: validationInspectDetailSchema,
		App:           taskAppRef(appRoot, cfg),
		Profile:       rec,
		Resolved:      plan.Steps,
		Tasks:         tasks,
		Diagnostics:   plan.Diagnostics,
		Source:        filepath.Join(appRoot, ".onlava.json"),
	}, nil
}

func buildValidationGraphResponse(appRoot string, cfg appcfg.Config, profile string) (validationGraphResponse, error) {
	profile = resolveValidationProfileName(cfg, profile)
	if _, ok := cfg.Validation.Profiles[profile]; !ok {
		return validationGraphResponse{}, fmt.Errorf("validation profile %q is not configured", profile)
	}
	resp := validationGraphResponse{
		SchemaVersion: validationGraphSchema,
		App:           taskAppRef(appRoot, cfg),
		Profile:       profile,
		Nodes:         []validationGraphNode{},
		Edges:         []validationGraphEdge{},
	}
	seen := map[string]bool{}
	var addNode func(validationGraphNode)
	addNode = func(node validationGraphNode) {
		if node.ID == "" || seen[node.ID] {
			return
		}
		seen[node.ID] = true
		resp.Nodes = append(resp.Nodes, node)
	}
	var walk func(name string, stack []string)
	walk = func(name string, stack []string) {
		id := "profile:" + name
		addNode(validationGraphNode{ID: id, Name: name, Kind: "profile", Source: ".onlava.json"})
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
			ref := parseValidationStep(step)
			if ref.Name == "" {
				continue
			}
			childID := ref.Kind + ":" + ref.Name
			if ref.Kind == "builtin" {
				childID = "builtin:" + ref.Name
			}
			addNode(validationGraphNode{ID: childID, Name: ref.Name, Kind: ref.Kind, Source: validationStepSource(ref)})
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
	resp.Diagnostics = append(resp.Diagnostics, validateValidationConfig(appRoot, cfg)...)
	return resp, nil
}

func buildValidationPlan(ctx context.Context, appRoot string, cfg appcfg.Config, opts validateOptions) (validationResolvedPlan, error) {
	selection := validationSelection{Mode: "explicit"}
	profile := resolveValidationProfileName(cfg, opts.Profile)
	if opts.Action == "changed" {
		files, err := collectValidationChangedFiles(ctx, appRoot, opts.Base)
		if err != nil {
			return validationResolvedPlan{}, err
		}
		selection.Mode = "changed"
		selection.Base = opts.Base
		selection.ChangedFiles = files
		profiles := selectChangedValidationProfiles(cfg, files)
		selection.Requested = profiles
		selection.MatchedProfiles = matchChangedValidationProfiles(cfg, files)
		if len(profiles) == 0 {
			profile = resolveValidationProfileName(cfg, "")
			selection.Requested = []string{profile}
		} else if len(profiles) == 1 {
			profile = profiles[0]
		} else {
			profile = strings.Join(profiles, "+")
		}
		return buildValidationMultiPlan(appRoot, cfg, profile, profiles, selection)
	}
	selection.Requested = []string{profile}
	return buildValidationNamedPlan(appRoot, cfg, profile, selection)
}

func buildValidationNamedPlan(appRoot string, cfg appcfg.Config, profile string, selection validationSelection) (validationResolvedPlan, error) {
	return buildValidationMultiPlan(appRoot, cfg, profile, []string{profile}, selection)
}

func buildValidationMultiPlan(appRoot string, cfg appcfg.Config, profileLabel string, profiles []string, selection validationSelection) (validationResolvedPlan, error) {
	plan := validationResolvedPlan{App: taskAppRef(appRoot, cfg), Profile: profileLabel, Selection: selection}
	plan.Diagnostics = append(plan.Diagnostics, validateValidationConfig(appRoot, cfg)...)
	seenProfiles := map[string]bool{}
	for _, profile := range profiles {
		if strings.TrimSpace(profile) == "" {
			continue
		}
		plan.addValidationProfile(appRoot, cfg, profile, nil, seenProfiles, nil)
	}
	plan.Selection.ResolvedProfiles = append([]string(nil), plan.Profiles...)
	return plan, nil
}

func (plan *validationResolvedPlan) addValidationProfile(appRoot string, cfg appcfg.Config, name string, stack []string, seenProfiles map[string]bool, inheritedEnv map[string]string) {
	if seenProfiles[name] {
		return
	}
	prof, ok := cfg.Validation.Profiles[name]
	if !ok {
		plan.Diagnostics = append(plan.Diagnostics, validationDiagnostic("validation", "error", "validation profile "+name+" is not configured"))
		return
	}
	for _, active := range stack {
		if active == name {
			plan.Diagnostics = append(plan.Diagnostics, validationDiagnostic("validation", "error", "profile cycle detected: "+strings.Join(append(stack, name), " -> ")))
			return
		}
	}
	seenProfiles[name] = true
	plan.Profiles = append(plan.Profiles, name)
	profileEnv := overlayStringMap(inheritedEnv, prof.Env)
	stack = append(stack, name)
	for idx, step := range prof.Steps {
		ref := parseValidationStep(step)
		if ref.Kind == "profile" {
			plan.addValidationProfile(appRoot, cfg, ref.Name, stack, seenProfiles, profileEnv)
			continue
		}
		stepID := strings.Join(append(stack, ref.Raw), "/")
		plan.Steps = append(plan.Steps, validationPlanStep{
			ID:       firstNonEmpty(stepID, fmt.Sprintf("%s/step-%d", name, idx+1)),
			Name:     ref.Raw,
			Kind:     ref.Kind,
			Profile:  name,
			Command:  validationStepCommand(appRoot, ref),
			CWD:      validationStepCWD(appRoot, cfg, ref),
			Artifact: append([]string(nil), prof.Artifacts...),
			Env:      profileEnv,
		})
	}
}

type validationStepRef struct {
	Raw  string
	Kind string
	Name string
}

func parseValidationStep(step string) validationStepRef {
	raw := strings.TrimSpace(step)
	switch {
	case strings.HasPrefix(raw, "profile:"):
		return validationStepRef{Raw: raw, Kind: "profile", Name: strings.TrimSpace(strings.TrimPrefix(raw, "profile:"))}
	case strings.HasPrefix(raw, "task:"):
		return validationStepRef{Raw: raw, Kind: "task", Name: strings.TrimSpace(strings.TrimPrefix(raw, "task:"))}
	default:
		return validationStepRef{Raw: raw, Kind: "builtin", Name: raw}
	}
}

func validationStepSource(ref validationStepRef) string {
	if ref.Kind == "builtin" {
		return "onlava"
	}
	return ".onlava.json"
}

func validationStepCommand(appRoot string, ref validationStepRef) []string {
	switch ref.Kind {
	case "task":
		return []string{"onlava", "task", "run", ref.Name, "--app-root", appRoot}
	case "builtin":
		switch ref.Name {
		case "harness", "harness:core":
			return []string{"onlava", "harness", "--app-root", appRoot, "--json"}
		case "harness:ui":
			return []string{"onlava", "harness", "ui", "--app-root", appRoot, "--json"}
		case "check":
			return []string{"onlava", "check", "--app-root", appRoot, "--json"}
		case "test", "test:go":
			return []string{"onlava", "test", "--app-root", appRoot}
		case "generate":
			return []string{"onlava", "generate", "--app-root", appRoot}
		case "generate:client":
			return []string{"onlava", "generate", "client", "--app-root", appRoot}
		case "generate:sqlc":
			return []string{"onlava", "generate", "sqlc", "--app-root", appRoot}
		case "db:apply":
			return []string{"onlava", "db", "apply", "--app-root", appRoot}
		case "db:seed":
			return []string{"onlava", "db", "seed", "--app-root", appRoot}
		case "db:setup":
			return []string{"onlava", "db", "setup", "--app-root", appRoot}
		}
	}
	return nil
}

func validationStepCWD(appRoot string, cfg appcfg.Config, ref validationStepRef) string {
	if ref.Kind == "task" {
		if task, ok := cfg.Tasks[ref.Name]; ok {
			return resolveLifecycleCWD(appRoot, task.CWD)
		}
	}
	return appRoot
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

func nonNilValidationDiagnostics(values []checkDiagnostic) []checkDiagnostic {
	if values == nil {
		return []checkDiagnostic{}
	}
	return values
}

func resolveValidationProfileName(cfg appcfg.Config, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	if strings.TrimSpace(cfg.Validation.Default) != "" {
		return strings.TrimSpace(cfg.Validation.Default)
	}
	if _, ok := cfg.Validation.Profiles["quick"]; ok {
		return "quick"
	}
	return ""
}

func validateValidationConfig(appRoot string, cfg appcfg.Config) []checkDiagnostic {
	var diags []checkDiagnostic
	for name, prof := range cfg.Validation.Profiles {
		if !validScriptSegment(name) || strings.Contains(name, ":") {
			diags = append(diags, validationDiagnostic("validation", "error", "invalid validation profile name "+name))
		}
		if validationReservedProfileName(name) {
			diags = append(diags, validationDiagnostic("validation", "error", "validation profile name "+name+" is reserved by onlava validate"))
		}
		if prof.Cost != "" && prof.Cost != "low" && prof.Cost != "medium" && prof.Cost != "high" {
			diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" has invalid cost "+prof.Cost))
		}
		if len(prof.Steps) == 0 {
			diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" has no steps"))
		}
		for _, glob := range prof.Paths {
			if strings.TrimSpace(glob) == "" {
				diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" has an empty path glob"))
			}
		}
		for _, step := range prof.Steps {
			ref := parseValidationStep(step)
			switch ref.Kind {
			case "profile":
				if ref.Name == "" {
					diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" has an empty profile step"))
				} else if _, ok := cfg.Validation.Profiles[ref.Name]; !ok {
					diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" references unknown profile "+ref.Name))
				}
			case "task":
				if ref.Name == "" {
					diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" has an empty task step"))
					continue
				}
				kind, err := taskTargetKind(ref.Name)
				if err != nil {
					diags = append(diags, validationDiagnostic("validation", "error", err.Error()))
				} else if kind == taskKindConfigured {
					if _, ok := cfg.Tasks[ref.Name]; !ok {
						diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" references unknown task "+ref.Name))
					}
				}
			case "builtin":
				if !validationBuiltinSupported(ref.Name) {
					diags = append(diags, validationDiagnostic("validation", "error", "validation profile "+name+" has unsupported step "+ref.Raw))
				}
			}
		}
	}
	defaultProfile := resolveValidationProfileName(cfg, "")
	if len(cfg.Validation.Profiles) > 0 && defaultProfile == "" {
		diags = append(diags, validationDiagnostic("validation", "error", "validation.default is not configured and profile quick does not exist"))
	} else if defaultProfile != "" {
		if _, ok := cfg.Validation.Profiles[defaultProfile]; !ok {
			diags = append(diags, validationDiagnostic("validation", "error", "validation default profile "+defaultProfile+" is not configured"))
		}
	}
	for _, cycle := range validationProfileCycles(cfg) {
		diags = append(diags, validationDiagnostic("validation", "error", "profile cycle detected: "+strings.Join(cycle, " -> ")))
	}
	for i := range diags {
		diags[i].File = filepath.Join(appRoot, ".onlava.json")
	}
	return diags
}

func validationReservedProfileName(name string) bool {
	switch name {
	case "list", "inspect", "graph", "changed":
		return true
	default:
		return false
	}
}

func validationDiagnostic(stage, severity, message string) checkDiagnostic {
	return checkDiagnostic{Stage: stage, Severity: severity, Message: message}
}

func validationBuiltinSupported(name string) bool {
	switch name {
	case "harness", "harness:core", "harness:ui", "check", "test", "test:go", "generate", "generate:client", "generate:sqlc", "db:apply", "db:seed", "db:setup":
		return true
	default:
		return false
	}
}

func validationProfileCycles(cfg appcfg.Config) [][]string {
	var cycles [][]string
	var walk func(name string, stack []string)
	walk = func(name string, stack []string) {
		for i, active := range stack {
			if active == name {
				cycle := append([]string(nil), stack[i:]...)
				cycle = append(cycle, name)
				cycles = append(cycles, cycle)
				return
			}
		}
		prof, ok := cfg.Validation.Profiles[name]
		if !ok {
			return
		}
		stack = append(stack, name)
		for _, step := range prof.Steps {
			ref := parseValidationStep(step)
			if ref.Kind == "profile" {
				walk(ref.Name, stack)
			}
		}
	}
	names := make([]string, 0, len(cfg.Validation.Profiles))
	for name := range cfg.Validation.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		walk(name, nil)
	}
	return cycles
}

func referencedValidationTasks(appRoot string, cfg appcfg.Config, steps []validationPlanStep) []taskListRecord {
	var out []taskListRecord
	seen := map[string]bool{}
	for _, step := range steps {
		ref := parseValidationStep(step.Name)
		if ref.Kind != "task" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		kind, err := taskTargetKind(ref.Name)
		if err != nil {
			continue
		}
		if kind == taskKindConfigured {
			if task, ok := cfg.Tasks[ref.Name]; ok {
				out = append(out, configuredTaskListRecord(appRoot, ref.Name, task))
			}
		}
	}
	return out
}

func resolvedProfileOrder(steps []validationPlanStep) []string {
	var out []string
	seen := map[string]bool{}
	for _, step := range steps {
		if step.Profile != "" && !seen[step.Profile] {
			seen[step.Profile] = true
			out = append(out, step.Profile)
		}
	}
	return out
}

func executeValidationPlan(ctx context.Context, appRoot string, cfg appcfg.Config, plan validationResolvedPlan, opts validateOptions) validationResultResponse {
	result := validationResultResponse{
		SchemaVersion: validationResultSchema,
		OK:            len(plan.Diagnostics) == 0,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		App:           plan.App,
		Profile:       plan.Profile,
		Selection:     plan.Selection,
		Diagnostics:   append([]checkDiagnostic(nil), plan.Diagnostics...),
		Steps:         []validationResultStep{},
	}
	artifactCtx := newValidationArtifactContext(appRoot, opts.Write)
	if len(plan.Diagnostics) > 0 {
		result.NextActions = []string{"Fix validation configuration diagnostics, then rerun: onlava validate " + plan.Profile + " --json --write"}
		return result
	}
	for _, step := range plan.Steps {
		res := runValidationStep(ctx, appRoot, cfg, step, artifactCtx, opts.JSON)
		result.Steps = append(result.Steps, res)
		if res.Evidence != nil {
			for _, artifact := range res.Evidence.Artifacts {
				kind := "artifact"
				if strings.Contains(artifact.Name, "stdout") {
					kind = "stdout"
				} else if strings.Contains(artifact.Name, "stderr") {
					kind = "stderr"
				}
				result.Artifacts = append(result.Artifacts, validationArtifact{Path: artifact.Path, Kind: kind})
			}
		}
		if !res.OK {
			result.OK = false
			result.NextActions = []string{"Fix " + res.Name + ", then rerun: onlava validate " + plan.Profile + " --json --write"}
			break
		}
	}
	return result
}

func runValidationStep(ctx context.Context, appRoot string, cfg appcfg.Config, step validationPlanStep, artifactCtx validationArtifactContext, capture bool) validationResultStep {
	started := time.Now()
	evidence := newHarnessEvidence(step.Command, firstNonEmpty(step.CWD, appRoot), started)
	var stdout, stderr bytes.Buffer
	err := runValidationStepCommand(ctx, appRoot, cfg, step, &stdout, &stderr, capture)
	ok := err == nil
	artifacts, diagnostics := writeValidationOutputArtifacts(artifactCtx, step.Name, stdout.Bytes(), stderr.Bytes())
	res := validationResultStep{
		ID:         step.ID,
		Name:       step.Name,
		Kind:       step.Kind,
		Profile:    step.Profile,
		OK:         ok,
		DurationMS: time.Since(started).Milliseconds(),
		Evidence:   &evidence,
	}
	if len(diagnostics) > 0 {
		res.Error = diagnostics[0].Message
		ok = false
		res.OK = false
	}
	if err != nil {
		res.Error = strings.TrimSpace(err.Error())
	}
	finalizeHarnessEvidence(res.Evidence, time.Since(started), ok, stdout.String(), stderr.String(), exitCodeFromError(err), artifacts)
	return res
}

func runValidationStepCommand(ctx context.Context, appRoot string, cfg appcfg.Config, step validationPlanStep, stdout, stderr io.Writer, capture bool) error {
	ref := parseValidationStep(step.Name)
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
			return runOnlavaHarness(ctx, stdout, []string{"--app-root", appRoot, "--json"})
		case "harness:ui":
			return runOnlavaHarnessUI(ctx, stdout, []string{"--app-root", appRoot, "--json"})
		case "check":
			return runOnlavaCheck(ctx, stdout, []string{"--app-root", appRoot, "--json"})
		case "test", "test:go":
			return runOnlavaTestOutput(ctx, []string{"--app-root", appRoot}, stdout)
		case "generate":
			return runGenerate(ctx, stdout, []string{"--app-root", appRoot})
		case "generate:client":
			return runGenerate(ctx, stdout, []string{"client", "--app-root", appRoot})
		case "generate:sqlc":
			return runGenerate(ctx, stdout, []string{"sqlc", "--app-root", appRoot})
		case "db:apply":
			if capture {
				return runWithCapturedProcessOutput(stdout, stderr, func() error {
					return dbApplyCommand([]string{"--app-root", appRoot})
				})
			}
			return dbApplyCommand([]string{"--app-root", appRoot})
		case "db:seed":
			if capture {
				return runWithCapturedProcessOutput(stdout, stderr, func() error {
					return dbSeedCommand([]string{"--app-root", appRoot})
				})
			}
			return dbSeedCommand([]string{"--app-root", appRoot})
		case "db:setup":
			if capture {
				return runWithCapturedProcessOutput(stdout, stderr, func() error {
					return dbSetupCommand([]string{"--app-root", appRoot})
				})
			}
			return dbSetupCommand([]string{"--app-root", appRoot})
		}
	}
	return fmt.Errorf("unsupported validation step %q", step.Name)
}

func runWithCapturedProcessOutput(stdout, stderr io.Writer, fn func() error) error {
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return err
	}
	os.Stdout = outW
	os.Stderr = errW
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdout, outR)
		close(outDone)
	}()
	go func() {
		_, _ = io.Copy(stderr, errR)
		close(errDone)
	}()
	runErr := fn()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	<-outDone
	<-errDone
	_ = outR.Close()
	_ = errR.Close()
	return runErr
}

func runValidationTask(ctx context.Context, appRoot string, cfg appcfg.Config, target string, stack []string, stdout, stderr io.Writer, envOverlay map[string]string) error {
	kind, err := taskTargetKind(target)
	if err != nil {
		return err
	}
	if kind == taskKindCode {
		return runOnlavaScript(ctx, scriptOptions{
			AppRoot:    appRoot,
			EnvOverlay: envOverlay,
			Target:     target,
			Stdout:     stdout,
			Stderr:     stderr,
			Stdin:      os.Stdin,
		})
	}
	task, ok := cfg.Tasks[target]
	if !ok {
		return fmt.Errorf("task %q is not configured", target)
	}
	for _, active := range stack {
		if active == target {
			return fmt.Errorf("task cycle detected: %s -> %s", strings.Join(stack, " -> "), target)
		}
	}
	stack = append(stack, target)
	if strings.TrimSpace(task.Run) != "" && len(task.Steps) > 0 {
		return fmt.Errorf("task %q cannot define both run and steps", target)
	}
	if strings.TrimSpace(task.Run) != "" {
		env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
		if err != nil {
			return err
		}
		env = overlayEnv(env, envOverlay)
		env = overlayEnv(env, task.Env)
		program, args := shellInvocation(task.Run)
		return runLifecycleExec(ctx, lifecycleExecRequest{
			Dir:     resolveLifecycleCWD(appRoot, task.CWD),
			Env:     env,
			Program: program,
			Args:    args,
			Stdin:   os.Stdin,
			Stdout:  stdout,
			Stderr:  stderr,
		})
	}
	if len(task.Steps) == 0 {
		return fmt.Errorf("task %q has no run command or steps", target)
	}
	for _, step := range task.Steps {
		ref := parseValidationStep(step)
		if ref.Kind == "task" {
			if err := runValidationTask(ctx, appRoot, cfg, ref.Name, stack, stdout, stderr, envOverlay); err != nil {
				return err
			}
			continue
		}
		planStep := validationPlanStep{Name: step, Kind: ref.Kind, CWD: appRoot, Command: validationStepCommand(appRoot, ref)}
		if err := runValidationStepCommand(ctx, appRoot, cfg, planStep, stdout, stderr, true); err != nil {
			return err
		}
	}
	return nil
}

func overlayStringMap(base, values map[string]string) map[string]string {
	if len(base) == 0 && len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(values))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func newValidationArtifactContext(root string, enabled bool) validationArtifactContext {
	return validationArtifactContext{
		Root:    root,
		Enabled: enabled,
		RunID:   time.Now().UTC().Format("20060102T150405.000000000Z"),
	}
}

func writeValidationOutputArtifacts(ctx validationArtifactContext, stepName string, stdout, stderr []byte) ([]harnessEvidenceArtifact, []checkDiagnostic) {
	if !ctx.Enabled {
		return nil, nil
	}
	var artifacts []harnessEvidenceArtifact
	var diags []checkDiagnostic
	write := func(kind string, data []byte) {
		if len(data) == 0 {
			return
		}
		name := sanitizeHarnessArtifactName(stepName + " " + kind)
		filename := sanitizeHarnessArtifactFilename(name + "." + kind + ".txt")
		rel := filepath.ToSlash(filepath.Join(".onlava", "harness", "validation", "artifacts", ctx.RunID, filename))
		abs := filepath.Join(ctx.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			diags = append(diags, formatArtifactWriteError(stepName, err))
			return
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			diags = append(diags, formatArtifactWriteError(stepName, err))
			return
		}
		artifacts = append(artifacts, harnessEvidenceArtifact{Name: name, Path: rel})
	}
	write("stdout", stdout)
	write("stderr", stderr)
	return artifacts, diags
}

func writeValidationResult(appRoot string, result *validationResultResponse) error {
	dir := filepath.Join(appRoot, ".onlava", "harness", "validation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	result.Wrote = filepath.Join(appRoot, ".onlava", "harness", "validation", "latest.json")
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

func selectChangedValidationProfiles(cfg appcfg.Config, files []string) []string {
	selected := []string{}
	defaultProfile := resolveValidationProfileName(cfg, "")
	if defaultProfile != "" {
		selected = append(selected, defaultProfile)
	}
	for _, match := range matchChangedValidationProfiles(cfg, files) {
		if !validationContainsString(selected, match.Profile) {
			selected = append(selected, match.Profile)
		}
	}
	return selected
}

func matchChangedValidationProfiles(cfg appcfg.Config, files []string) []validationProfileMatch {
	names := make([]string, 0, len(cfg.Validation.Profiles))
	for name := range cfg.Validation.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var matches []validationProfileMatch
	for _, name := range names {
		prof := cfg.Validation.Profiles[name]
		if len(prof.Paths) == 0 {
			continue
		}
		var matchedPaths, matchedFiles []string
		for _, pattern := range prof.Paths {
			for _, file := range files {
				if validationGlobMatches(pattern, file) {
					if !validationContainsString(matchedPaths, pattern) {
						matchedPaths = append(matchedPaths, pattern)
					}
					if !validationContainsString(matchedFiles, file) {
						matchedFiles = append(matchedFiles, file)
					}
				}
			}
		}
		if len(matchedFiles) > 0 {
			matches = append(matches, validationProfileMatch{Profile: name, MatchedPaths: matchedPaths, MatchedFiles: matchedFiles})
		}
	}
	return matches
}

func validationContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validationGlobMatches(pattern, file string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	file = filepath.ToSlash(strings.TrimSpace(file))
	if pattern == "" || file == "" {
		return false
	}
	if pattern == file {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return file == prefix || strings.HasPrefix(file, prefix+"/")
	}
	if validationGlobMatchSegments(strings.Split(pattern, "/"), strings.Split(file, "/")) {
		return true
	}
	ok, _ := filepath.Match(pattern, file)
	if ok {
		return true
	}
	if !strings.Contains(pattern, "/") {
		ok, _ = filepath.Match(pattern, filepath.Base(file))
		return ok
	}
	return false
}

func validationGlobMatchSegments(patternParts, fileParts []string) bool {
	if len(patternParts) == 0 {
		return len(fileParts) == 0
	}
	if patternParts[0] == "**" {
		if validationGlobMatchSegments(patternParts[1:], fileParts) {
			return true
		}
		for i := range fileParts {
			if validationGlobMatchSegments(patternParts[1:], fileParts[i+1:]) {
				return true
			}
		}
		return false
	}
	if len(fileParts) == 0 {
		return false
	}
	ok, err := filepath.Match(patternParts[0], fileParts[0])
	if err != nil || !ok {
		return false
	}
	return validationGlobMatchSegments(patternParts[1:], fileParts[1:])
}
