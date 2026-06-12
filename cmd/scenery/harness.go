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
	"scenery.sh/internal/inspect"
)

type harnessOptions struct {
	AppRoot           string
	JSON              bool
	Write             bool
	WithValidation    bool
	ValidationProfile string
}

type harnessResponse struct {
	SchemaVersion string             `json:"schema_version"`
	OK            bool               `json:"ok"`
	GeneratedAt   string             `json:"generated_at"`
	App           inspect.AppRef     `json:"app"`
	Knowledge     harnessKnowledge   `json:"knowledge"`
	Steps         []harnessStep      `json:"steps"`
	Artifacts     []harnessArtifact  `json:"artifacts"`
	Validation    *harnessValidation `json:"validation,omitempty"`
	NextActions   []string           `json:"next_actions,omitempty"`
	Wrote         string             `json:"wrote,omitempty"`
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
		SchemaVersion: "scenery.harness.result.v1",
		OK:            true,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		App: inspect.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".scenery.json"),
		},
		Knowledge: buildHarnessKnowledge(appRoot),
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

	for _, subject := range []string{"app", "routes", "services", "endpoints", "models", "views", "wire", "build", "paths"} {
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
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return harnessOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--write":
			opts.Write = true
		case "--with-validation":
			opts.WithValidation = true
		default:
			if strings.HasPrefix(args[i], "--with-validation=") {
				opts.WithValidation = true
				opts.ValidationProfile = strings.TrimSpace(strings.TrimPrefix(args[i], "--with-validation="))
				if opts.ValidationProfile == "" {
					return harnessOptions{}, fmt.Errorf("--with-validation profile must not be empty")
				}
				continue
			}
			return harnessOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func runHarnessValidation(ctx context.Context, appRoot, profile string) harnessValidation {
	args := []string{"--app-root", appRoot, "--json", "--write"}
	if strings.TrimSpace(profile) != "" {
		args = append([]string{profile}, args...)
	}
	var out bytes.Buffer
	err := runSceneryValidate(ctx, &out, args)
	var result validationResultResponse
	if json.Unmarshal(out.Bytes(), &result) == nil && result.SchemaVersion == validationResultSchema {
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
	command := []string{"scenery", "check", "--app-root", appRoot, "--json"}
	evidence := newHarnessEvidence(command, appRoot, started)
	var out bytes.Buffer
	err := runSceneryCheck(ctx, &out, []string{"--app-root", appRoot, "--json"})
	step := harnessStep{
		Name:       "check",
		Command:    command,
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
		Evidence:   &evidence,
	}
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, "check.json", "scenery.check.result.v1", out.Bytes(), nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)

	var payload checkResponse
	if out.Len() > 0 && json.Unmarshal(out.Bytes(), &payload) == nil {
		step.OK = payload.OK
		step.Diagnostics = append(step.Diagnostics, payload.Diagnostics...)
		step.Summary = map[string]any{
			"diagnostics": len(payload.Diagnostics),
		}
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step, payload.App
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
	command := []string{"scenery", "inspect", subject, "--app-root", appRoot, "--json"}
	evidence := newHarnessEvidence(command, appRoot, started)
	var out bytes.Buffer
	err := runSceneryInspect([]string{subject, "--app-root", appRoot, "--json"}, &out)
	step := harnessStep{
		Name:       "inspect " + subject,
		Command:    command,
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
		Evidence:   &evidence,
	}
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, "inspect-"+subject+".json", "scenery.inspect."+subject+".v1", out.Bytes(), nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
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
	command := []string{"scenery", subject, "list", "--app-root", appRoot, "--json"}
	evidence := newHarnessEvidence(command, appRoot, started)
	switch subject {
	case "traces":
		err = runObservabilityList(context.Background(), &out, "traces", []string{"--app-root", appRoot, "--json"})
	case "metrics":
		err = runObservabilityList(context.Background(), &out, "metrics", []string{"--app-root", appRoot, "--json"})
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
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, subject+".json", "scenery.inspect."+subject+".v1", out.Bytes(), nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, out.String(), "", exitCodeFromError(err), artifacts)
		return step
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
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
	if schema, _ := payload["schema_version"].(string); schema != "" {
		summary["schema_version"] = schema
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
		if wire, ok := payload["wire"].(map[string]any); ok {
			summary["wire"] = wire
		}
	case "wire":
		if tools, ok := payload["tools"].([]any); ok {
			summary["tools"] = len(tools)
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
		"docs/schemas/scenery.config.v1.schema.json",
		"docs/schemas/scenery.check.result.v1.schema.json",
		"docs/schemas/scenery.harness.artifact.v1.schema.json",
		"docs/schemas/scenery.harness.result.v1.schema.json",
		"docs/schemas/scenery.inspect.harness.v1.schema.json",
		"docs/schemas/scenery.inspect.app.v1.schema.json",
		"docs/schemas/scenery.inspect.routes.v1.schema.json",
		"docs/schemas/scenery.inspect.services.v1.schema.json",
		"docs/schemas/scenery.inspect.endpoints.v1.schema.json",
		"docs/schemas/scenery.inspect.models.v1.schema.json",
		"docs/schemas/scenery.inspect.views.v1.schema.json",
		"docs/schemas/scenery.inspect.traces.v1.schema.json",
		"docs/schemas/scenery.inspect.metrics.v1.schema.json",
		"docs/schemas/scenery.inspect.observability.v1.schema.json",
		"docs/schemas/scenery.logs.query.v1.schema.json",
		"docs/schemas/scenery.logs.tail.entry.v1.schema.json",
		"docs/schemas/scenery.metrics.query.v1.schema.json",
		"docs/schemas/scenery.metrics.labels.v1.schema.json",
		"docs/schemas/scenery.metrics.series.v1.schema.json",
		"docs/schemas/scenery.wire.capabilities.v1.schema.json",
		"docs/schemas/scenery.inspect.build.v1.schema.json",
		"docs/schemas/scenery.inspect.paths.v1.schema.json",
		"docs/schemas/scenery.inspect.temporal.v1.schema.json",
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
		{Name: "app", Path: ".scenery/gen/app.json", SchemaVersion: "scenery.inspect.app.v1"},
		{Name: "routes", Path: ".scenery/gen/routes.json", SchemaVersion: "scenery.inspect.routes.v1"},
		{Name: "services", Path: ".scenery/gen/services.json", SchemaVersion: "scenery.inspect.services.v1"},
		{Name: "endpoints", Path: ".scenery/gen/endpoints.json", SchemaVersion: "scenery.inspect.endpoints.v1"},
		{Name: "models", Path: ".scenery/gen/models.json", SchemaVersion: "scenery.inspect.models.v1"},
		{Name: "views", Path: ".scenery/gen/views.json", SchemaVersion: "scenery.inspect.views.v1"},
		{Name: "wire", Path: ".scenery/gen/wire/capabilities.json", SchemaVersion: "scenery.wire.capabilities.v1"},
		{Name: "gen-manifest", Path: ".scenery/gen/manifest.json", SchemaVersion: "scenery.gen.manifest.v1"},
		{Name: "latest-build", Path: ".scenery/build/latest.json", SchemaVersion: "scenery.build.latest.v1"},
		{Name: "latest-harness", Path: ".scenery/harness/latest.json", SchemaVersion: "scenery.harness.result.v1"},
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
	if err := writeHarnessJSON(&buf, resp); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return nil
}

func writeHarnessJSON(w io.Writer, payload harnessResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
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
