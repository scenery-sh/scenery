package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/storagefs"
)

type inspectOptions struct {
	Subject      string
	ReportToken  string
	AppRoot      string
	RepoRoot     string
	JSON         bool
	StorageStats bool
	UI           inspectUIOptions
	Trace        inspectTraceQueryOptions
	Harness      inspectHarnessOptions
}

type inspectBuildResponse struct {
	cliPayloadIdentity
	App   inspectdata.AppRef `json:"app"`
	Build inspectBuildRecord `json:"build"`
}

type inspectBuildRecord struct {
	WorkspaceDir          string `json:"workspace_dir"`
	BinaryPath            string `json:"binary_path"`
	WorkspaceExists       bool   `json:"workspace_exists"`
	BinaryExists          bool   `json:"binary_exists"`
	BuildStatePath        string `json:"build_state_path"`
	BuildStateExists      bool   `json:"build_state_exists"`
	BuildStateVersion     string `json:"build_state_version,omitempty"`
	DependencyFingerprint string `json:"dependency_fingerprint,omitempty"`
	GraphFingerprint      string `json:"graph_fingerprint,omitempty"`
	MetadataPresent       bool   `json:"metadata_present"`
	APIEncodingPresent    bool   `json:"api_encoding_present"`
	SourceFileCount       int    `json:"source_file_count"`
	GeneratedFileCount    int    `json:"generated_file_count"`
}

type inspectStorageResponse struct {
	cliPayloadIdentity
	App     inspectdata.AppRef    `json:"app"`
	Storage inspectStorageRecord  `json:"storage"`
	Stores  []inspectStorageStore `json:"stores"`
}

type inspectStorageRecord struct {
	Configured bool                    `json:"configured"`
	Declared   bool                    `json:"declared"`
	Default    string                  `json:"default,omitempty"`
	Readiness  string                  `json:"readiness"`
	Scope      storageResponseScope    `json:"scope"`
	Recovery   *storagefs.RecoveryInfo `json:"recovery,omitempty"`
	Totals     *storagefs.Stats        `json:"totals,omitempty"`
}

type inspectStorageStore struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Access         string `json:"access"`
	TenantScoped   bool   `json:"tenant_scoped"`
	MaxObjectBytes int64  `json:"max_object_bytes,omitempty"`
	ObjectCount    *int64 `json:"object_count,omitempty"`
	TotalBytes     *int64 `json:"total_bytes,omitempty"`
}

func inspectCommand(args []string) error {
	return runSceneryInspect(args, os.Stdout)
}

func runSceneryInspect(args []string, stdout io.Writer) error {
	opts, err := parseInspectArgs(args)
	if err != nil {
		return inspectInvalidRequest(err)
	}
	if !opts.JSON && opts.Subject != "ui" && opts.Subject != "storage" {
		return inspectInvalidRequest(fmt.Errorf("scenery inspect currently requires -o json"))
	}

	if opts.Subject == "report" {
		report, err := readFailureReport(opts.ReportToken)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, report)
	}
	if opts.Subject == "harness" {
		if opts.Harness.Topic != "" {
			resp, err := buildInspectHarnessFocusedResponse(opts)
			if err != nil {
				return err
			}
			return writeInspectJSON(stdout, resp)
		}
		resp, err := buildInspectHarnessResponse(opts)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	}

	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		if opts.Subject == "storage" && filepath.IsAbs(opts.AppRoot) {
			plan, retainedErr := retainedStorageNamespacePlan(opts.AppRoot)
			if retainedErr != nil {
				return retainedErr
			}
			return runInspectStorage(context.Background(), stdout, plan, appcfg.Config{ID: plan.Binding.AppID, Name: plan.Binding.AppID}, opts)
		}
		return err
	}
	switch opts.Subject {
	case "routes":
		merged, err := compileSourcePlan(context.Background(), appRoot)
		if err != nil {
			return err
		}
		if !merged.Valid() {
			return writeInspectCompileFailure(stdout, merged)
		}
		response, err := buildInspectRoutesResponse(appRoot, cfg, merged)
		if err != nil {
			return err
		}
		return writeSourceResult(stdout, "json", false, merged, response)
	case "build":
		resp, err := buildInspectBuildResponse(appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "storage":
		plan, err := resolveStorageNamespacePlan(cfg, appRoot, "")
		if err != nil {
			return err
		}
		return runInspectStorage(context.Background(), stdout, plan, cfg, opts)
	case "ui":
		resp, err := buildInspectUIResponse(appRoot, cfg, opts.UI.Frontend)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		return writeInspectUIHuman(stdout, resp)
	default:
		return inspectInvalidRequest(fmt.Errorf("unknown inspect subject %q", opts.Subject))
	}
}

func inspectInvalidRequest(err error) error {
	if err == nil || strings.HasPrefix(err.Error(), "invalid_request:") {
		return err
	}
	return fmt.Errorf("invalid_request: %w", err)
}

// writeInspectCompileFailure emits the failure envelope with the real
// compiler diagnostics; wrapping them in a plain error would make the CLI
// render an opaque SCN9000 internal-failure diagnostic instead.
func writeInspectCompileFailure(stdout io.Writer, result *compiler.Result) error {
	diagnostics := result.Diagnostics
	if diagnostics == nil {
		diagnostics = []graph.Diagnostic{}
	}
	if err := json.NewEncoder(stdout).Encode(newCLIEnvelope(false, nil, diagnostics)); err != nil {
		return err
	}
	return &silentCLIError{
		err:  fmt.Errorf("merged graph is invalid: %s", firstCompilerDiagnostic(diagnostics)),
		code: contractInvalidExitCode(result),
	}
}

func firstCompilerDiagnostic(diagnostics []graph.Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return diagnostic.Code + ": " + diagnostic.Message + compilerDiagnosticLocation(diagnostic)
		}
	}
	return "unknown compilation failure"
}

// compilerDiagnosticLocation renders " (path:line:column)" so single-line
// build errors point at the offending file; Range alone only carries an
// opaque hashed source id.
func compilerDiagnosticLocation(diagnostic graph.Diagnostic) string {
	if diagnostic.Path == "" {
		return ""
	}
	location := diagnostic.Path
	if diagnostic.Range != nil {
		location = fmt.Sprintf("%s:%d:%d", location, diagnostic.Range.Start.Line, diagnostic.Range.Start.Column)
	}
	return " (" + location + ")"
}

func parseInspectArgs(args []string) (inspectOptions, error) {
	return parseInspectArgsInternal(args, false)
}

func parseInspectArgsInternal(args []string, allowObservability bool) (inspectOptions, error) {
	if len(args) == 0 {
		return inspectOptions{}, usageErrorf("missing inspect subject")
	}
	if err := flagBeforeWord(args, "the inspect subject"); err != nil {
		return inspectOptions{}, err
	}
	opts := inspectOptions{Subject: args[0]}
	if !allowObservability {
		switch opts.Subject {
		case "traces":
			return inspectOptions{}, usageErrorf("unknown inspect subject %q; use `scenery traces list`", opts.Subject)
		case "metrics":
			return inspectOptions{}, usageErrorf("unknown inspect subject %q; use `scenery metrics`", opts.Subject)
		}
	}
	flags := newCLIFlagSet("inspect " + opts.Subject)
	registerJSONOutput(flags, &opts.JSON)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.RepoRoot, "repo-root", "", "")
	flags.BoolVar(&opts.StorageStats, "stats", false, "")
	flags.StringVar(&opts.UI.Frontend, "frontend", "", "")
	flags.StringVar(&opts.Harness.Severity, "severity", "", "")
	flags.IntVar(&opts.Harness.Top, "top", 0, "")
	traceValues := map[string]*string{}
	for _, name := range []string{"limit", "since", "service", "endpoint", "trace-id", "session", "status", "min-duration-ms"} {
		value := new(string)
		traceValues[name] = value
		flags.StringVar(value, name, "", "")
	}
	flags.BoolVar(&opts.Trace.Slowest, "slowest", false, "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return inspectOptions{}, err
	}
	if cliFlagSet(flags, "repo-root") && opts.Subject != "harness" {
		return inspectOptions{}, usageErrorf("--repo-root is only supported for inspect harness")
	}
	if cliFlagSet(flags, "stats") && opts.Subject != "storage" {
		return inspectOptions{}, usageErrorf("--stats requires inspect storage")
	}
	if cliFlagSet(flags, "frontend") && opts.Subject != "ui" {
		return inspectOptions{}, usageErrorf("--frontend is only supported for inspect ui")
	}
	if opts.Subject == "report" {
		if len(positionals) == 0 {
			return inspectOptions{}, usageErrorf("missing report token; usage: scenery inspect report <report-token> -o json")
		}
		opts.ReportToken, positionals = positionals[0], positionals[1:]
	}
	if opts.Subject == "harness" && len(positionals) > 0 {
		opts.Harness.Topic = positionals[0]
		if opts.Harness.Topic != "artifact" && opts.Harness.Topic != "diagnostics" && opts.Harness.Topic != "timing" {
			return inspectOptions{}, usageErrorf("unknown inspect harness topic %q; use artifact, diagnostics or timing", positionals[0])
		}
		positionals = positionals[1:]
		if opts.Harness.Topic == "artifact" {
			if len(positionals) == 0 {
				return inspectOptions{}, usageErrorf("missing inspect harness artifact name")
			}
			opts.Harness.Name, positionals = positionals[0], positionals[1:]
		}
	}
	if len(positionals) > 0 {
		return inspectOptions{}, usageErrorf("unexpected argument %q", positionals[0])
	}
	if cliFlagSet(flags, "severity") && (opts.Subject != "harness" || opts.Harness.Topic != "diagnostics") {
		return inspectOptions{}, usageErrorf("--severity is only supported for inspect harness diagnostics")
	}
	if cliFlagSet(flags, "severity") && opts.Harness.Severity != "error" && opts.Harness.Severity != "warning" {
		return inspectOptions{}, usageErrorf("--severity must be error or warning")
	}
	if cliFlagSet(flags, "top") && (opts.Subject != "harness" || opts.Harness.Topic != "timing") {
		return inspectOptions{}, usageErrorf("--top is only supported for inspect harness timing")
	}
	if cliFlagSet(flags, "top") && opts.Harness.Top <= 0 {
		return inspectOptions{}, usageErrorf("--top must be a positive integer")
	}
	for _, name := range []string{"limit", "since", "service", "endpoint", "trace-id", "session", "status", "min-duration-ms"} {
		if !cliFlagSet(flags, name) {
			continue
		}
		if opts.Subject != "traces" && opts.Subject != "metrics" {
			return inspectOptions{}, usageErrorf("--%s is only supported for traces list and metrics", name)
		}
		if err := parseInspectTraceFlags(&opts, "--"+name, *traceValues[name]); err != nil {
			return inspectOptions{}, err
		}
	}
	if cliFlagSet(flags, "slowest") && opts.Subject != "traces" && opts.Subject != "metrics" {
		return inspectOptions{}, usageErrorf("--slowest is only supported for traces list and metrics")
	}
	return opts, nil
}

func writeInspectJSON(w io.Writer, payload any) error {
	return writeCLIJSON(w, payload)
}

func buildInspectBuildResponse(appRoot string, cfg appcfg.Config) (inspectBuildResponse, error) {
	if manifest, ok, err := build.ReadLatestBuildManifest(appRoot); err != nil {
		return inspectBuildResponse{}, err
	} else if ok {
		return inspectBuildResponse{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.inspect.build"),
			App: inspectdata.AppRef{
				Name:       manifest.App.Name,
				ID:         manifest.App.ID,
				Root:       manifest.App.Root,
				ConfigPath: manifest.App.ConfigPath,
			},
			Build: inspectBuildRecord{
				WorkspaceDir:          manifest.Build.WorkspaceDir,
				BinaryPath:            manifest.Build.BinaryPath,
				WorkspaceExists:       manifest.Build.WorkspaceExists,
				BinaryExists:          manifest.Build.BinaryExists,
				BuildStatePath:        manifest.Build.BuildStatePath,
				BuildStateExists:      manifest.Build.BuildStateExists,
				BuildStateVersion:     manifest.Build.BuildStateVersion,
				DependencyFingerprint: manifest.Build.DependencyFingerprint,
				GraphFingerprint:      manifest.Build.GraphFingerprint,
				MetadataPresent:       manifest.Build.MetadataPresent,
				APIEncodingPresent:    manifest.Build.APIEncodingPresent,
				SourceFileCount:       manifest.Build.SourceFileCount,
				GeneratedFileCount:    manifest.Build.GeneratedFileCount,
			},
		}, nil
	}

	cacheRoot, err := commandCacheRoot()
	if err != nil {
		return inspectBuildResponse{}, err
	}
	workspaceDir, err := build.WorkspaceDirAt(cacheRoot, appRoot, cfg.Name)
	if err != nil {
		return inspectBuildResponse{}, err
	}
	state, err := build.ReadStateInfo(appRoot, cfg.Name)
	if err != nil {
		return inspectBuildResponse{}, err
	}
	binaryPath := filepath.Join(workspaceDir, "scenery-app")
	resp := inspectBuildResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.inspect.build"),
		App:                inspectAppInfo(appRoot, cfg, nil),
		Build: inspectBuildRecord{
			WorkspaceDir:          workspaceDir,
			BinaryPath:            binaryPath,
			WorkspaceExists:       pathExists(workspaceDir),
			BinaryExists:          pathExists(binaryPath),
			BuildStatePath:        state.Path,
			BuildStateExists:      state.Exists,
			BuildStateVersion:     state.Version,
			DependencyFingerprint: state.DependencyFingerprint,
			GraphFingerprint:      state.GraphFingerprint,
			MetadataPresent:       state.MetadataPresent,
			APIEncodingPresent:    state.APIEncodingPresent,
			SourceFileCount:       len(state.SourceFiles),
			GeneratedFileCount:    len(state.GeneratedFiles),
		},
	}
	return resp, nil
}

func inspectAppInfo(appRoot string, cfg appcfg.Config, _ any) inspectdata.AppRef {
	return inspectdata.AppRef{
		Name:       cfg.Name,
		ID:         cfg.ID,
		Root:       appRoot,
		ConfigPath: cfg.SourcePath(appRoot),
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
