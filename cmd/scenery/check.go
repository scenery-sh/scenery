package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/build"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/workers"
)

type checkOptions struct {
	AppRoot string
	JSON    bool
}

type checkResponse struct {
	SchemaVersion string             `json:"schema_version"`
	OK            bool               `json:"ok"`
	App           inspectdata.AppRef `json:"app"`
	Diagnostics   []checkDiagnostic  `json:"diagnostics"`
}

type checkDiagnostic struct {
	Stage           string `json:"stage"`
	Severity        string `json:"severity"`
	File            string `json:"file,omitempty"`
	Line            int    `json:"line,omitempty"`
	Column          int    `json:"column,omitempty"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

var checkDiagnosticRE = regexp.MustCompile(`^(.+?):([0-9]+)(?::([0-9]+))?:\s*(.+)$`)

func checkCommand(args []string) error {
	return runSceneryCheck(context.Background(), os.Stdout, args)
}

func runSceneryCheck(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseCheckArgs(args)
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
	appInfo := inspectdata.AppRef{
		Name:       cfg.Name,
		ID:         cfg.ID,
		Root:       appRoot,
		ConfigPath: filepath.Join(appRoot, ".scenery.json"),
	}

	snapshot, snapshotErr := scanWatchedFiles(appRoot)
	graphFingerprint := ""
	if snapshotErr == nil {
		graphFingerprint = snapshotFingerprint(snapshot)
		if cached, cachedApp, err := cachedCheckResult(appRoot, cfg, graphFingerprint); err != nil {
			return renderCheckFailure(stdout, opts.JSON, appInfo, "cache", err)
		} else if cached {
			if cachedApp.ModulePath != "" {
				appInfo.ModulePath = cachedApp.ModulePath
			}
			return renderCheckSuccess(stdout, opts.JSON, appInfo)
		}
	}

	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return renderCheckFailure(stdout, opts.JSON, appInfo, "parse", err)
	}
	appInfo.ModulePath = model.ModulePath
	if cfg.Temporal.Enabled {
		if diagnostics := typeScriptTemporalDiagnostics(appRoot, model); len(diagnostics) > 0 {
			return renderCheckFailure(stdout, opts.JSON, appInfo, "temporal-typescript", workers.DiagnosticsError(diagnostics))
		}
	}

	result, err := build.Prepare(appRoot, model, cfg)
	if err != nil {
		return renderCheckFailure(stdout, opts.JSON, appInfo, "prepare", err)
	}
	if graphFingerprint != "" {
		result.GraphFingerprint = graphFingerprint
	}
	if err := build.CompileContext(ctx, result); err != nil {
		return renderCheckFailure(stdout, opts.JSON, appInfo, "compile", err)
	}
	return renderCheckSuccess(stdout, opts.JSON, appInfo)
}

func renderCheckSuccess(stdout io.Writer, jsonMode bool, appInfo inspectdata.AppRef) error {
	if jsonMode {
		return writeCheckJSON(stdout, checkResponse{
			SchemaVersion: "scenery.check.result.v1",
			OK:            true,
			App:           appInfo,
			Diagnostics:   []checkDiagnostic{},
		})
	}
	_, _ = fmt.Fprintln(stdout, "scenery: check ok")
	return nil
}

func cachedCheckResult(appRoot string, cfg appcfg.Config, graphFingerprint string) (bool, inspectdata.AppRef, error) {
	if graphFingerprint == "" {
		return false, inspectdata.AppRef{}, nil
	}
	manifest, ok, err := build.ReadLatestBuildManifest(appRoot)
	if err != nil || !ok {
		return false, inspectdata.AppRef{}, err
	}
	if manifest.SchemaVersion != "scenery.build.latest.v1" || manifest.Build.Phase != "compiled" {
		return false, inspectdata.AppRef{}, nil
	}
	if manifest.App.Name != cfg.Name || manifest.App.Root != appRoot {
		return false, inspectdata.AppRef{}, nil
	}
	if strings.TrimSpace(cfg.ID) != "" && strings.TrimSpace(manifest.App.ID) != strings.TrimSpace(cfg.ID) {
		return false, inspectdata.AppRef{}, nil
	}
	state, err := build.ReadStateInfo(appRoot, cfg.Name)
	if err != nil {
		return false, inspectdata.AppRef{}, err
	}
	if !state.Exists || state.Version == "" || state.Version != manifest.Build.BuildStateVersion || state.GraphFingerprint != graphFingerprint {
		return false, inspectdata.AppRef{}, nil
	}
	if !pathExists(manifest.Build.BinaryPath) || !pathExists(manifest.Build.BuildStatePath) {
		return false, inspectdata.AppRef{}, nil
	}
	if toolIsNewerThanBuild(appRoot) {
		return false, inspectdata.AppRef{}, nil
	}
	app, ok, err := inspectdata.ReadGeneratedApp(appRoot)
	if err != nil || !ok {
		return false, inspectdata.AppRef{}, err
	}
	if _, ok, err := inspectdata.ReadGeneratedRoutes(appRoot); err != nil || !ok {
		return false, inspectdata.AppRef{}, err
	}
	if _, ok, err := inspectdata.ReadGeneratedServices(appRoot); err != nil || !ok {
		return false, inspectdata.AppRef{}, err
	}
	if _, ok, err := inspectdata.ReadGeneratedEndpoints(appRoot); err != nil || !ok {
		return false, inspectdata.AppRef{}, err
	}
	if _, ok, err := inspectdata.ReadGeneratedWireCapabilities(appRoot); err != nil || !ok {
		return false, inspectdata.AppRef{}, err
	}
	return true, app.App, nil
}

func toolIsNewerThanBuild(appRoot string) bool {
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	exeInfo, err := os.Stat(exe)
	if err != nil {
		return true
	}
	manifestInfo, err := os.Stat(build.LatestBuildPath(appRoot))
	if err != nil {
		return true
	}
	return exeInfo.ModTime().After(manifestInfo.ModTime().Add(time.Second))
}

func renderCheckFailure(stdout io.Writer, jsonMode bool, app inspectdata.AppRef, stage string, cause error) error {
	if !jsonMode {
		return cause
	}
	resp := checkResponse{
		SchemaVersion: "scenery.check.result.v1",
		OK:            false,
		App:           app,
		Diagnostics:   buildCheckDiagnostics(app.Root, stage, cause),
	}
	if len(resp.Diagnostics) == 0 {
		resp.Diagnostics = []checkDiagnostic{{
			Stage:           stage,
			Severity:        "error",
			Message:         strings.TrimSpace(cause.Error()),
			SuggestedAction: suggestedActionForDiagnostic(stage, strings.TrimSpace(cause.Error())),
		}}
	}
	if err := writeCheckJSON(stdout, resp); err != nil {
		return err
	}
	return &silentCLIError{err: cause}
}

func parseCheckArgs(args []string) (checkOptions, error) {
	opts := checkOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return checkOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			return checkOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func writeCheckJSON(w io.Writer, payload checkResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func buildCheckDiagnostics(appRoot, stage string, err error) []checkDiagnostic {
	lines := strings.Split(strings.ReplaceAll(err.Error(), "\r\n", "\n"), "\n")
	diags := make([]checkDiagnostic, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || shouldSkipDiagnosticLine(line, stage) {
			continue
		}
		diag := checkDiagnostic{
			Stage:           stage,
			Severity:        "error",
			Message:         line,
			SuggestedAction: suggestedActionForDiagnostic(stage, line),
		}
		if match := checkDiagnosticRE.FindStringSubmatch(line); match != nil {
			diag.File = normalizeDiagnosticFile(appRoot, match[1])
			diag.Line = parseDiagnosticInt(match[2])
			diag.Column = parseDiagnosticInt(match[3])
			diag.Message = match[4]
			diag.SuggestedAction = suggestedActionForDiagnostic(stage, diag.Message)
		}
		key := fmt.Sprintf("%s|%s|%d|%d|%s", diag.Stage, diag.File, diag.Line, diag.Column, diag.Message)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		diags = append(diags, diag)
	}
	return diags
}

func shouldSkipDiagnosticLine(line, stage string) bool {
	switch {
	case strings.HasPrefix(line, "go build "),
		strings.HasPrefix(line, "go mod tidy "),
		strings.HasPrefix(line, "go test "),
		strings.HasPrefix(line, "exit status "):
		return true
	case stage == "compile" && strings.HasPrefix(line, "# "):
		return true
	default:
		return false
	}
}

func normalizeDiagnosticFile(appRoot, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if rel, err := filepath.Rel(appRoot, value); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(value)
}

func parseDiagnosticInt(value string) int {
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func suggestedActionForDiagnostic(stage, message string) string {
	switch {
	case strings.Contains(message, "undefined:"):
		return "Define the missing symbol or add the required import, then rerun `scenery check --json`."
	case strings.Contains(message, "no matching files found"):
		return "Ensure the referenced file exists at build time and rerun `scenery check --json`."
	case strings.Contains(message, "updates to go.mod needed"):
		return "Run `go mod tidy` in the app and rerun `scenery check --json`."
	case stage == "parse":
		return "Fix the source or scenery directive error, then rerun `scenery check --json`."
	case stage == "prepare":
		return "Fix the generated workspace or dependency setup issue, then rerun `scenery check --json`."
	case stage == "temporal-typescript":
		return "Fix the TypeScript Temporal worker declaration or matching Go external activity, then rerun `scenery check --json`."
	default:
		return "Fix the compile error, then rerun `scenery check --json`."
	}
}
