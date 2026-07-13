package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/inspect"
	"scenery.sh/internal/machine"
)

type harnessOptions struct {
	AppRoot           string
	JSON              bool
	Write             bool
	WithValidation    bool
	ValidationProfile string
}

type harnessResponse struct {
	cliPayloadIdentity
	OK          bool               `json:"ok"`
	GeneratedAt string             `json:"generated_at"`
	App         inspect.AppRef     `json:"app"`
	Knowledge   harnessKnowledge   `json:"knowledge"`
	Steps       []harnessStep      `json:"steps"`
	Artifacts   []harnessArtifact  `json:"artifacts"`
	Validation  *harnessValidation `json:"validation,omitempty"`
	NextActions []string           `json:"next_actions,omitempty"`
	Wrote       string             `json:"wrote,omitempty"`
}

type harnessValidation struct {
	Profile    string `json:"profile"`
	OK         bool   `json:"ok"`
	ResultPath string `json:"result_path"`
}

type harnessKnowledge struct {
	Entrypoints []harnessKnowledgeFile `json:"entrypoints"`
	Schemas     []harnessKnowledgeFile `json:"schemas"`
}

type harnessKnowledgeFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type harnessStep struct {
	Name        string            `json:"name"`
	Command     []string          `json:"command"`
	OK          bool              `json:"ok"`
	DurationMS  int64             `json:"duration_ms"`
	Evidence    *harnessEvidence  `json:"evidence,omitempty"`
	Effects     []string          `json:"effects,omitempty"`
	Summary     map[string]any    `json:"summary,omitempty"`
	Diagnostics []checkDiagnostic `json:"diagnostics,omitempty"`
	Error       string            `json:"error,omitempty"`
	OutputTail  string            `json:"output_tail,omitempty"`
}

func harnessCommand(args []string) error {
	return runSceneryHarness(context.Background(), os.Stdout, args)
}

func runSceneryHarness(ctx context.Context, stdout io.Writer, args []string) error {
	if len(args) > 0 && args[0] == "self" {
		return runSceneryHarnessSelf(ctx, stdout, args[1:])
	}
	if len(args) > 0 && args[0] == "ui" {
		return runSceneryHarnessUI(ctx, stdout, args[1:])
	}

	opts, err := parseHarnessArgs(args)
	if err != nil {
		return err
	}

	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}

	resp := harnessResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.harness.result"),
		OK:                 true,
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339Nano),
		App: inspect.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: cfg.SourcePath(appRoot),
		},
		Knowledge: buildHarnessKnowledge(appRoot),
	}
	if contract, compileErr := compiler.Compile(appRoot); compileErr == nil && contract.Manifest != nil {
		resp.App = inspectAppRef(appRoot, cfg, contract)
	}
	artifactCtx := newHarnessArtifactContext(appRoot, opts.Write)

	checkStep, checkApp := runHarnessCheck(ctx, appRoot, artifactCtx)
	if checkApp.ModulePath != "" {
		resp.App.ModulePath = checkApp.ModulePath
	}
	resp.Steps = append(resp.Steps, checkStep)
	if !checkStep.OK {
		resp.OK = false
	}

	for _, subject := range []string{"app", "routes", "services", "endpoints", "build", "paths"} {
		step := runHarnessInspect(subject, appRoot, artifactCtx)
		resp.Steps = append(resp.Steps, step)
		if !step.OK {
			resp.OK = false
		}
	}
	for _, subject := range []string{"traces", "metrics"} {
		step := runHarnessObservability(subject, appRoot, artifactCtx)
		resp.Steps = append(resp.Steps, step)
		if !step.OK {
			resp.OK = false
		}
	}

	annotateHarnessEvidence(resp.Steps, appRoot)
	resp.NextActions = buildHarnessNextActions(resp.Steps)

	if opts.Write {
		resp.Wrote = filepath.Join(appRoot, ".scenery", "harness", "latest.json")
	}
	resp.Artifacts = buildHarnessArtifacts(appRoot, opts.Write)

	if opts.WithValidation {
		validation := runHarnessValidation(ctx, appRoot, opts.ValidationProfile)
		resp.Validation = &validation
		if !validation.OK {
			resp.OK = false
		}
	}

	if opts.Write {
		if err := writeHarnessResult(resp.Wrote, resp); err != nil {
			return err
		}
	}

	if opts.JSON {
		if err := writeHarnessJSON(stdout, resp); err != nil {
			return err
		}
		if !resp.OK {
			return &silentCLIError{err: fmt.Errorf("scenery harness failed")}
		}
		return nil
	}

	if err := writeHarnessText(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("scenery harness failed")
	}
	return nil
}

func parseHarnessArgs(args []string) (harnessOptions, error) {
	opts := harnessOptions{}
	flags := newCLIFlagSet("harness")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.Write, "write", false, "")
	flags.BoolFunc("with-validation", "", func(value string) error {
		opts.WithValidation = true
		if value != "true" {
			opts.ValidationProfile = strings.TrimSpace(value)
			if opts.ValidationProfile == "" {
				return fmt.Errorf("--with-validation profile must not be empty")
			}
		}
		return nil
	})
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return harnessOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return harnessOptions{}, err
	}
	return opts, nil
}

func runHarnessValidation(ctx context.Context, appRoot, profile string) harnessValidation {
	args := []string{"--app-root", appRoot, "-o", "json", "--write"}
	if strings.TrimSpace(profile) != "" {
		args = append([]string{profile}, args...)
	}
	var out bytes.Buffer
	err := runSceneryValidate(ctx, &out, args)
	var result validationResultResponse
	if decodeCLIJSON(out.Bytes(), &result) == nil && result.cliPayloadIdentity == newCLIPayloadIdentity(validationResultKind) {
		resultPath := ".scenery/harness/validation/latest.json"
		if result.Wrote != "" {
			if rel, relErr := filepath.Rel(appRoot, result.Wrote); relErr == nil {
				resultPath = filepath.ToSlash(rel)
			}
		}
		return harnessValidation{Profile: result.Profile, OK: result.OK && err == nil, ResultPath: resultPath}
	}
	return harnessValidation{Profile: profile, OK: false, ResultPath: ".scenery/harness/validation/latest.json"}
}

func runHarnessCheck(ctx context.Context, appRoot string, artifactCtxs ...harnessArtifactContext) (harnessStep, inspect.AppRef) {
	started := time.Now()
	checkArgs := []string{"--app-root", appRoot, "-o", "json"}
	command := append([]string{"scenery", "check"}, checkArgs...)
	evidence := newHarnessEvidence(command, appRoot, started)
	var out bytes.Buffer
	err := runSceneryCheck(ctx, &out, checkArgs)
	step := harnessStep{
		Name:       "check",
		Command:    command,
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
		Evidence:   &evidence,
	}
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, "check.json", machine.EnvelopeKind, out.Bytes(), nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)

	payload, decodeErr := machine.Decode[graph.Diagnostic](out.Bytes(), currentMachineSpecRevision())
	if out.Len() > 0 && decodeErr == nil {
		step.OK = payload.OK && err == nil
		step.Summary = map[string]any{
			"diagnostics": len(payload.Diagnostics),
		}
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step, inspect.AppRef{}
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.OK = false
	}
	finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
	return step, inspect.AppRef{}
}

func runHarnessInspect(subject, appRoot string, artifactCtxs ...harnessArtifactContext) harnessStep {
	started := time.Now()
	command := []string{"scenery", "inspect", subject, "--app-root", appRoot, "-o", "json"}
	evidence := newHarnessEvidence(command, appRoot, started)
	var out bytes.Buffer
	err := runSceneryInspect([]string{subject, "--app-root", appRoot, "-o", "json"}, &out)
	step := harnessStep{
		Name:       "inspect " + subject,
		Command:    command,
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
		Evidence:   &evidence,
	}
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, "inspect-"+subject+".json", "scenery.inspect."+subject, out.Bytes(), nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step
	}
	var payload map[string]any
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		step.OK = false
		step.Error = "invalid inspect JSON: " + err.Error()
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step
	}
	step.Summary = summarizeHarnessInspect(subject, payload)
	finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
	return step
}

func runHarnessObservability(subject, appRoot string, artifactCtxs ...harnessArtifactContext) harnessStep {
	started := time.Now()
	var out bytes.Buffer
	var err error
	command := []string{"scenery", subject, "list", "--app-root", appRoot, "-o", "json"}
	evidence := newHarnessEvidence(command, appRoot, started)
	switch subject {
	case "traces":
		err = runObservabilityList(context.Background(), &out, "traces", []string{"--app-root", appRoot, "-o", "json"})
	case "metrics":
		err = runObservabilityList(context.Background(), &out, "metrics", []string{"--app-root", appRoot, "-o", "json"})
	default:
		err = fmt.Errorf("unknown observability subject %q", subject)
	}
	step := harnessStep{
		Name:       subject + " list",
		Command:    command,
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
		Evidence:   &evidence,
	}
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, subject+".json", "scenery.inspect."+subject, out.Bytes(), nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step
	}
	var payload map[string]any
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		step.OK = false
		step.Error = "invalid observability JSON: " + err.Error()
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step
	}
	step.Summary = summarizeHarnessInspect(subject, payload)
	finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
	return step
}

func summarizeHarnessInspect(subject string, payload map[string]any) map[string]any {
	summary := map[string]any{}
	if kind, _ := payload["kind"].(string); kind != "" {
		summary["kind"] = kind
	}
	if revision, _ := payload["schema_revision"].(string); revision != "" {
		summary["schema_revision"] = revision
	}
	switch subject {
	case "app":
		if counts, ok := payload["counts"].(map[string]any); ok {
			for key, value := range counts {
				summary[key] = value
			}
		}
	case "routes":
		if items, ok := payload["routes"].([]any); ok {
			summary["routes"] = len(items)
		}
	case "services":
		if items, ok := payload["services"].([]any); ok {
			summary["services"] = len(items)
		}
	case "endpoints":
		if items, ok := payload["endpoints"].([]any); ok {
			summary["endpoints"] = len(items)
		}
	case "build":
		if buildInfo, ok := payload["build"].(map[string]any); ok {
			for _, key := range []string{"workspace_dir", "binary_path", "latest_manifest_path", "compiled"} {
				if value, ok := buildInfo[key]; ok {
					summary[key] = value
				}
			}
		}
	case "paths":
		if paths, ok := payload["paths"].(map[string]any); ok {
			for _, key := range []string{"app_root", "cache_root", "workspace_dir", "build_state_path"} {
				if value, ok := paths[key]; ok {
					summary[key] = value
				}
			}
		}
	case "traces":
		if items, ok := payload["traces"].([]any); ok {
			summary["traces"] = len(items)
		}
	case "metrics":
		if metrics, ok := payload["summary"].(map[string]any); ok {
			for _, key := range []string{"trace_count", "error_count", "event_count", "log_count", "avg_duration_ms", "p95_duration_ms"} {
				if value, ok := metrics[key]; ok {
					summary[key] = value
				}
			}
		}
	}
	return summary
}

func buildHarnessKnowledge(appRoot string) harnessKnowledge {
	entrypoints := []string{
		"AGENTS.md",
		"docs/local-contract.md",
		"docs/agent-guide.md",
	}
	schemas := []string{
		"docs/schemas/scenery.config.schema.json",
		"docs/schemas/scenery.cli.schema.json",
		"docs/schemas/scenery.harness.artifact.schema.json",
		"docs/schemas/scenery.harness.result.schema.json",
		"docs/schemas/scenery.inspect.harness.schema.json",
		"docs/schemas/scenery.inspect.app.schema.json",
		"docs/schemas/scenery.inspect.routes.schema.json",
		"docs/schemas/scenery.inspect.services.schema.json",
		"docs/schemas/scenery.inspect.endpoints.schema.json",
		"docs/schemas/scenery.inspect.traces.schema.json",
		"docs/schemas/scenery.inspect.metrics.schema.json",
		"docs/schemas/scenery.inspect.observability.schema.json",
		"docs/schemas/scenery.logs.query.schema.json",
		"docs/schemas/scenery.logs.tail.entry.schema.json",
		"docs/schemas/scenery.metrics.query.schema.json",
		"docs/schemas/scenery.metrics.labels.schema.json",
		"docs/schemas/scenery.metrics.series.schema.json",
		"docs/schemas/scenery.inspect.build.schema.json",
		"docs/schemas/scenery.inspect.paths.schema.json",
	}
	return harnessKnowledge{
		Entrypoints: harnessKnowledgeFiles(appRoot, entrypoints),
		Schemas:     harnessKnowledgeFiles(appRoot, schemas),
	}
}

func harnessKnowledgeFiles(root string, relPaths []string) []harnessKnowledgeFile {
	files := make([]harnessKnowledgeFile, 0, len(relPaths))
	for _, rel := range relPaths {
		_, err := os.Stat(filepath.Join(root, rel))
		files = append(files, harnessKnowledgeFile{
			Path:   filepath.ToSlash(rel),
			Exists: err == nil,
		})
	}
	return files
}

func buildHarnessArtifacts(appRoot string, harnessWillExist bool) []harnessArtifact {
	artifacts := []harnessArtifact{
		{Name: "latest-build", Path: ".scenery/build/latest.json"},
		newHarnessArtifact("latest-harness", ".scenery/harness/latest.json", "scenery.harness.result", false),
	}
	for i := range artifacts {
		if artifacts[i].Name == "latest-harness" && harnessWillExist {
			artifacts[i].Exists = true
			continue
		}
		_, err := os.Stat(filepath.Join(appRoot, filepath.FromSlash(artifacts[i].Path)))
		artifacts[i].Exists = err == nil
	}
	return artifacts
}

func buildHarnessNextActions(steps []harnessStep) []string {
	seen := make(map[string]struct{})
	var actions []string
	for _, step := range steps {
		if step.OK {
			continue
		}
		for _, diag := range step.Diagnostics {
			action := strings.TrimSpace(diag.SuggestedAction)
			if action == "" {
				continue
			}
			if _, ok := seen[action]; ok {
				continue
			}
			seen[action] = struct{}{}
			actions = append(actions, action)
		}
		if step.Error != "" {
			action := "Fix `" + step.Name + "`: " + step.Error
			if _, ok := seen[action]; !ok {
				seen[action] = struct{}{}
				actions = append(actions, action)
			}
		}
	}
	return actions
}

func writeHarnessResult(path string, resp harnessResponse) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return nil
}

func writeHarnessJSON(w io.Writer, payload harnessResponse) error {
	return writeCLIJSON(w, payload)
}

func writeHarnessText(w io.Writer, resp harnessResponse) error {
	status := "ok"
	if !resp.OK {
		status = "failed"
	}
	if _, err := fmt.Fprintf(w, "scenery: harness %s for %s\n", status, resp.App.Name); err != nil {
		return err
	}
	for _, step := range resp.Steps {
		marker := "ok"
		if !step.OK {
			marker = "failed"
		}
		if _, err := fmt.Fprintf(w, "  %s %-18s duration_ms=%d\n", marker, step.Name, step.DurationMS); err != nil {
			return err
		}
	}
	if resp.Wrote != "" {
		_, _ = fmt.Fprintf(w, "  wrote %s\n", resp.Wrote)
	}
	return nil
}
