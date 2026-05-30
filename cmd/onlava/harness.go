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

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/inspect"
)

type harnessOptions struct {
	AppRoot string
	JSON    bool
	Write   bool
}

type harnessResponse struct {
	SchemaVersion string            `json:"schema_version"`
	OK            bool              `json:"ok"`
	GeneratedAt   string            `json:"generated_at"`
	App           inspect.AppRef    `json:"app"`
	Knowledge     harnessKnowledge  `json:"knowledge"`
	Steps         []harnessStep     `json:"steps"`
	Artifacts     []harnessArtifact `json:"artifacts"`
	NextActions   []string          `json:"next_actions,omitempty"`
	Wrote         string            `json:"wrote,omitempty"`
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
	Effects     []string          `json:"effects,omitempty"`
	Summary     map[string]any    `json:"summary,omitempty"`
	Diagnostics []checkDiagnostic `json:"diagnostics,omitempty"`
	Error       string            `json:"error,omitempty"`
	OutputTail  string            `json:"output_tail,omitempty"`
}

type harnessArtifact struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version,omitempty"`
	Exists        bool   `json:"exists"`
}

func harnessCommand(args []string) error {
	return runOnlavaHarness(context.Background(), os.Stdout, args)
}

func runOnlavaHarness(ctx context.Context, stdout io.Writer, args []string) error {
	if len(args) > 0 && args[0] == "self" {
		return runOnlavaHarnessSelf(ctx, stdout, args[1:])
	}
	if len(args) > 0 && args[0] == "ui" {
		return runOnlavaHarnessUI(ctx, stdout, args[1:])
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
		SchemaVersion: "onlava.harness.result.v1",
		OK:            true,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		App: inspect.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".onlava.json"),
		},
		Knowledge: buildHarnessKnowledge(appRoot),
	}

	checkStep, checkApp := runHarnessCheck(ctx, appRoot)
	if checkApp.ModulePath != "" {
		resp.App.ModulePath = checkApp.ModulePath
	}
	resp.Steps = append(resp.Steps, checkStep)
	if !checkStep.OK {
		resp.OK = false
	}

	for _, subject := range []string{"app", "routes", "services", "endpoints", "wire", "build", "paths", "traces", "metrics"} {
		step := runHarnessInspect(subject, appRoot)
		resp.Steps = append(resp.Steps, step)
		if !step.OK {
			resp.OK = false
		}
	}

	resp.NextActions = buildHarnessNextActions(resp.Steps)

	if opts.Write {
		resp.Wrote = filepath.Join(appRoot, ".onlava", "harness", "latest.json")
	}
	resp.Artifacts = buildHarnessArtifacts(appRoot, opts.Write)

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
			return &silentCLIError{err: fmt.Errorf("onlava harness failed")}
		}
		return nil
	}

	if err := writeHarnessText(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("onlava harness failed")
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
		default:
			return harnessOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func runHarnessCheck(ctx context.Context, appRoot string) (harnessStep, inspect.AppRef) {
	started := time.Now()
	var out bytes.Buffer
	err := runOnlavaCheck(ctx, &out, []string{"--app-root", appRoot, "--json"})
	step := harnessStep{
		Name:       "check",
		Command:    []string{"onlava", "check", "--app-root", appRoot, "--json"},
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
	}

	var payload checkResponse
	if out.Len() > 0 && json.Unmarshal(out.Bytes(), &payload) == nil {
		step.OK = payload.OK
		step.Diagnostics = payload.Diagnostics
		step.Summary = map[string]any{
			"diagnostics": len(payload.Diagnostics),
		}
		return step, payload.App
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.OK = false
	}
	return step, inspect.AppRef{}
}

func runHarnessInspect(subject, appRoot string) harnessStep {
	started := time.Now()
	var out bytes.Buffer
	err := runOnlavaInspect([]string{subject, "--app-root", appRoot, "--json"}, &out)
	step := harnessStep{
		Name:       "inspect " + subject,
		Command:    []string{"onlava", "inspect", subject, "--app-root", appRoot, "--json"},
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		return step
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		step.OK = false
		step.Error = "invalid inspect JSON: " + err.Error()
		return step
	}
	step.Summary = summarizeHarnessInspect(subject, payload)
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
		"docs/PRD-2-proto.md",
	}
	schemas := []string{
		"docs/schemas/onlava.config.v1.schema.json",
		"docs/schemas/onlava.check.result.v1.schema.json",
		"docs/schemas/onlava.harness.result.v1.schema.json",
		"docs/schemas/onlava.inspect.app.v1.schema.json",
		"docs/schemas/onlava.inspect.routes.v1.schema.json",
		"docs/schemas/onlava.inspect.services.v1.schema.json",
		"docs/schemas/onlava.inspect.endpoints.v1.schema.json",
		"docs/schemas/onlava.inspect.traces.v1.schema.json",
		"docs/schemas/onlava.inspect.metrics.v1.schema.json",
		"docs/schemas/onlava.wire.capabilities.v1.schema.json",
		"docs/schemas/onlava.inspect.build.v1.schema.json",
		"docs/schemas/onlava.inspect.paths.v1.schema.json",
		"docs/schemas/onlava.inspect.temporal.v1.schema.json",
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
		{Name: "app", Path: ".onlava/gen/app.json", SchemaVersion: "onlava.inspect.app.v1"},
		{Name: "routes", Path: ".onlava/gen/routes.json", SchemaVersion: "onlava.inspect.routes.v1"},
		{Name: "services", Path: ".onlava/gen/services.json", SchemaVersion: "onlava.inspect.services.v1"},
		{Name: "endpoints", Path: ".onlava/gen/endpoints.json", SchemaVersion: "onlava.inspect.endpoints.v1"},
		{Name: "wire", Path: ".onlava/gen/wire/capabilities.json", SchemaVersion: "onlava.wire.capabilities.v1"},
		{Name: "gen-manifest", Path: ".onlava/gen/manifest.json", SchemaVersion: "onlava.gen.manifest.v1"},
		{Name: "latest-build", Path: ".onlava/build/latest.json", SchemaVersion: "onlava.build.latest.v1"},
		{Name: "latest-harness", Path: ".onlava/harness/latest.json", SchemaVersion: "onlava.harness.result.v1"},
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
	if _, err := fmt.Fprintf(w, "onlava: harness %s for %s\n", status, resp.App.Name); err != nil {
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
