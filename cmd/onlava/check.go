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

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
	"github.com/pbrazdil/onlava/internal/parse"
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
	return runOnlavaCheck(context.Background(), os.Stdout, args)
}

func runOnlavaCheck(ctx context.Context, stdout io.Writer, args []string) error {
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
		ConfigPath: filepath.Join(appRoot, ".onlava.json"),
	}

	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return renderCheckFailure(stdout, opts.JSON, appInfo, "parse", err)
	}
	appInfo.ModulePath = model.ModulePath

	result, err := build.Prepare(appRoot, model, cfg, build.PrepareOptions{})
	if err != nil {
		return renderCheckFailure(stdout, opts.JSON, appInfo, "prepare", err)
	}
	if err := build.CompileContext(ctx, result); err != nil {
		return renderCheckFailure(stdout, opts.JSON, appInfo, "compile", err)
	}
	if opts.JSON {
		return writeCheckJSON(stdout, checkResponse{
			SchemaVersion: "onlava.check.result.v1",
			OK:            true,
			App:           appInfo,
			Diagnostics:   []checkDiagnostic{},
		})
	}
	_, _ = fmt.Fprintln(stdout, "onlava: check ok")
	return nil
}

func renderCheckFailure(stdout io.Writer, jsonMode bool, app inspectdata.AppRef, stage string, cause error) error {
	if !jsonMode {
		return cause
	}
	resp := checkResponse{
		SchemaVersion: "onlava.check.result.v1",
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
		return "Define the missing symbol or add the required import, then rerun `onlava check --json`."
	case strings.Contains(message, "no matching files found"):
		return "Ensure the referenced file exists at build time and rerun `onlava check --json`."
	case strings.Contains(message, "updates to go.mod needed"):
		return "Run `go mod tidy` in the app and rerun `onlava check --json`."
	case stage == "parse":
		return "Fix the source or onlava directive error, then rerun `onlava check --json`."
	case stage == "prepare":
		return "Fix the generated workspace or dependency setup issue, then rerun `onlava check --json`."
	default:
		return "Fix the compile error, then rerun `onlava check --json`."
	}
}
