package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
	"github.com/pbrazdil/onlava/internal/wiremodel"
	"github.com/pbrazdil/onlava/internal/workers"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

var inspectAppModelCache = struct {
	sync.Mutex
	items map[string]*inspectAppModelCacheEntry
}{
	items: map[string]*inspectAppModelCacheEntry{},
}

type inspectAppModelCacheEntry struct {
	ready chan struct{}
	app   *model.App
	err   error
}

type inspectOptions struct {
	Subject  string
	AppRoot  string
	RepoRoot string
	JSON     bool
	Trace    inspectTraceQueryOptions
	Harness  inspectHarnessOptions
}

type inspectBuildResponse struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Build         inspectBuildRecord `json:"build"`
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

type inspectPathsResponse struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Paths         inspectPathsRecord `json:"paths"`
}

type inspectPathsRecord struct {
	AppRoot        string `json:"app_root"`
	ConfigPath     string `json:"config_path"`
	CacheRoot      string `json:"cache_root"`
	BuildRoot      string `json:"build_root"`
	WorkspaceDir   string `json:"workspace_dir"`
	BinaryPath     string `json:"binary_path"`
	BuildStatePath string `json:"build_state_path"`
}

type inspectTemporalResponse struct {
	SchemaVersion   string                `json:"schema_version"`
	App             inspectdata.AppRef    `json:"app"`
	Temporal        inspectTemporalRecord `json:"temporal"`
	Declarations    []temporalDeclaration `json:"declarations"`
	TypeScript      temporalTypeScript    `json:"typescript"`
	Connectivity    temporalConnectivity  `json:"connectivity"`
	WorkerManifests workers.Validation    `json:"worker_manifests"`
}

type inspectTemporalRecord struct {
	Enabled          bool   `json:"enabled"`
	Mode             string `json:"mode"`
	Address          string `json:"address"`
	AddressEnv       string `json:"address_env"`
	AddressEnvSet    bool   `json:"address_env_set"`
	Namespace        string `json:"namespace"`
	NamespaceEnvSet  bool   `json:"namespace_env_set"`
	TaskQueuePrefix  string `json:"task_queue_prefix"`
	TaskQueueEnv     string `json:"task_queue_env"`
	TaskQueueEnvSet  bool   `json:"task_queue_env_set"`
	PayloadCodec     string `json:"payload_codec"`
	APIKeyEnv        string `json:"api_key_env"`
	APIKeyEnvSet     bool   `json:"api_key_env_set"`
	TLSEnabled       bool   `json:"tls_enabled"`
	TLSServerNameEnv string `json:"tls_server_name_env"`
	TLSServerNameSet bool   `json:"tls_server_name_env_set"`
	TLSCACertFileEnv string `json:"tls_ca_cert_file_env"`
	TLSCACertFileSet bool   `json:"tls_ca_cert_file_env_set"`
	TLSCertFileEnv   string `json:"tls_cert_file_env"`
	TLSCertFileSet   bool   `json:"tls_cert_file_env_set"`
	TLSKeyFileEnv    string `json:"tls_key_file_env"`
	TLSKeyFileSet    bool   `json:"tls_key_file_env_set"`
	HostReporting    bool   `json:"host_resource_reporting"`
	HostReportingEnv string `json:"host_resource_reporting_env"`
	HostReportingSet bool   `json:"host_resource_reporting_env_set"`
	DeploymentName   string `json:"deployment_name"`
	DeploymentEnv    string `json:"deployment_env"`
	DeploymentEnvSet bool   `json:"deployment_env_set"`
	WorkerBuildID    string `json:"worker_build_id"`
	WorkerBuildIDEnv string `json:"worker_build_id_env"`
	WorkerBuildIDSet bool   `json:"worker_build_id_set"`
	Versioning       string `json:"versioning"`
	VersioningEnv    string `json:"versioning_env"`
	VersioningEnvSet bool   `json:"versioning_env_set"`
	LocalAutoStart   bool   `json:"local_auto_start"`
	LocalDBFilename  string `json:"local_db_filename"`
	ConnectTimeoutMS int64  `json:"connect_timeout_ms"`
}

type temporalConnectivity struct {
	Checked   bool   `json:"checked"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

type temporalDeclaration struct {
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	TaskQueue         string `json:"task_queue"`
	TaskQueueExplicit bool   `json:"task_queue_explicit"`
	File              string `json:"file"`
	Line              int    `json:"line"`
}

type temporalTypeScript struct {
	Checked      bool                         `json:"checked"`
	OK           bool                         `json:"ok"`
	GeneratedDir string                       `json:"generated_dir"`
	Activities   []temporalTypeScriptActivity `json:"activities"`
	Diagnostics  []workers.Diagnostic         `json:"diagnostics,omitempty"`
}

type temporalTypeScriptActivity struct {
	Name           string `json:"name"`
	TaskQueue      string `json:"task_queue"`
	ExportName     string `json:"export_name"`
	Input          string `json:"input"`
	Output         string `json:"output"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

func inspectCommand(args []string) error {
	return runOnlavaInspect(args, os.Stdout)
}

func runOnlavaInspect(args []string, stdout io.Writer) error {
	opts, err := parseInspectArgs(args)
	if err != nil {
		return err
	}
	if !opts.JSON {
		return fmt.Errorf("onlava inspect currently requires --json")
	}

	if opts.Subject == "docs" {
		repoRoot, err := discoverOnlavaRepoRoot(opts.RepoRoot)
		if err != nil {
			return err
		}
		resp, err := buildInspectDocsResponse(repoRoot)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
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
		return err
	}

	switch opts.Subject {
	case "app":
		if payload, ok, err := inspectdata.ReadGeneratedApp(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, inspectdata.BuildAppResponse(appRoot, cfg, model))
	case "services":
		if payload, ok, err := inspectdata.ReadGeneratedServices(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, inspectdata.BuildServicesResponse(appRoot, cfg, model))
	case "routes":
		if payload, ok, err := inspectdata.ReadGeneratedRoutes(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, inspectdata.BuildRoutesResponse(appRoot, cfg, model))
	case "endpoints":
		if payload, ok, err := inspectdata.ReadGeneratedEndpoints(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, inspectdata.BuildEndpointsResponse(appRoot, cfg, model))
	case "wire":
		if payload, ok, err := inspectdata.ReadGeneratedWireCapabilities(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, wiremodel.AppCapabilities(model))
	case "build":
		resp, err := buildInspectBuildResponse(appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "paths":
		resp, err := buildInspectPathsResponse(appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "generators":
		resp, err := buildInspectGeneratorsResponse(appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "temporal":
		resp, err := buildInspectTemporalResponse(context.Background(), appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "validation":
		return writeInspectJSON(stdout, buildInspectValidationResponse(appRoot, cfg))
	case "observability":
		resp, err := buildInspectObservabilityResponse(context.Background(), appRoot, cfg, opts.Trace.Session)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	default:
		return fmt.Errorf("unknown inspect subject %q", opts.Subject)
	}
}

func parseInspectArgs(args []string) (inspectOptions, error) {
	return parseInspectArgsInternal(args, false)
}

func parseInspectArgsInternal(args []string, allowObservability bool) (inspectOptions, error) {
	if len(args) == 0 {
		return inspectOptions{}, fmt.Errorf("missing inspect subject")
	}
	opts := inspectOptions{Subject: args[0]}
	if !allowObservability && (opts.Subject == "traces" || opts.Subject == "metrics") {
		return inspectOptions{}, fmt.Errorf("unknown inspect subject %q; use `onlava %s list`", opts.Subject, opts.Subject)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--app-root":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--repo-root":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --repo-root")
			}
			if opts.Subject != "docs" && opts.Subject != "harness" {
				return inspectOptions{}, fmt.Errorf("--repo-root is only supported for inspect docs and inspect harness")
			}
			opts.RepoRoot = args[i]
		case "--severity":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --severity")
			}
			if opts.Subject != "harness" || opts.Harness.Topic != "diagnostics" {
				return inspectOptions{}, fmt.Errorf("--severity is only supported for inspect harness diagnostics")
			}
			opts.Harness.Severity = args[i]
		case "--top":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --top")
			}
			if opts.Subject != "harness" || opts.Harness.Topic != "timing" {
				return inspectOptions{}, fmt.Errorf("--top is only supported for inspect harness timing")
			}
			top, err := strconv.Atoi(args[i])
			if err != nil || top <= 0 {
				return inspectOptions{}, fmt.Errorf("--top must be a positive integer")
			}
			opts.Harness.Top = top
		case "--limit", "-n", "--since", "--service", "--endpoint", "--trace-id", "--session", "--status", "--min-duration-ms":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for %s", args[i-1])
			}
			if args[i-1] == "--session" && opts.Subject == "observability" {
				opts.Trace.Session = strings.TrimSpace(args[i])
				if opts.Trace.Session == "" {
					return inspectOptions{}, fmt.Errorf("invalid session %q", args[i])
				}
				continue
			}
			if opts.Subject != "traces" && opts.Subject != "metrics" {
				return inspectOptions{}, fmt.Errorf("%s is only supported for traces list and metrics list", args[i-1])
			}
			if err := parseInspectTraceFlags(&opts, args[i-1], args[i]); err != nil {
				return inspectOptions{}, err
			}
		case "--slowest":
			if opts.Subject != "traces" && opts.Subject != "metrics" {
				return inspectOptions{}, fmt.Errorf("%s is only supported for traces list and metrics list", args[i])
			}
			opts.Trace.Slowest = true
		case "artifact", "diagnostics", "timing":
			if opts.Subject != "harness" {
				return inspectOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			if opts.Harness.Topic != "" {
				return inspectOptions{}, fmt.Errorf("only one inspect harness topic may be selected")
			}
			opts.Harness.Topic = args[i]
			if args[i] == "artifact" {
				i++
				if i >= len(args) {
					return inspectOptions{}, fmt.Errorf("missing inspect harness artifact name")
				}
				opts.Harness.Name = args[i]
			}
		default:
			return inspectOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func writeInspectJSON(w io.Writer, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func cachedInspectAppModel(appRoot, appName string) (*model.App, error) {
	key, err := inspectAppModelCacheKey(appRoot, appName)
	if err != nil {
		return nil, err
	}
	inspectAppModelCache.Lock()
	if entry := inspectAppModelCache.items[key]; entry != nil {
		inspectAppModelCache.Unlock()
		<-entry.ready
		return entry.app, entry.err
	}
	entry := &inspectAppModelCacheEntry{ready: make(chan struct{})}
	inspectAppModelCache.items[key] = entry
	inspectAppModelCache.Unlock()

	appModel, err := parse.App(appRoot, appName)

	inspectAppModelCache.Lock()
	entry.app = appModel
	entry.err = err
	if err != nil {
		delete(inspectAppModelCache.items, key)
	}
	close(entry.ready)
	inspectAppModelCache.Unlock()

	return appModel, err
}

func inspectAppModelCacheKey(appRoot, appName string) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte(appName))
	_, _ = h.Write([]byte{0})
	err := filepath.WalkDir(appRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".":
				return nil
			case ".onlava", "node_modules", "dist", "out":
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case rel == ".onlava.json", rel == "go.mod", rel == "go.sum", strings.HasSuffix(rel, ".go"):
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func buildInspectBuildResponse(appRoot string, cfg appcfg.Config) (inspectBuildResponse, error) {
	if manifest, ok, err := build.ReadLatestBuildManifest(appRoot); err != nil {
		return inspectBuildResponse{}, err
	} else if ok {
		return inspectBuildResponse{
			SchemaVersion: "onlava.inspect.build.v1",
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

	workspaceDir, err := build.WorkspaceDir(appRoot, cfg.Name)
	if err != nil {
		return inspectBuildResponse{}, err
	}
	state, err := build.ReadStateInfo(appRoot, cfg.Name)
	if err != nil {
		return inspectBuildResponse{}, err
	}
	binaryPath := filepath.Join(workspaceDir, "onlava-app")
	resp := inspectBuildResponse{
		SchemaVersion: "onlava.inspect.build.v1",
		App:           inspectAppInfo(appRoot, cfg, nil),
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

func buildInspectPathsResponse(appRoot string, cfg appcfg.Config) (inspectPathsResponse, error) {
	cacheRoot, err := build.CacheRoot()
	if err != nil {
		return inspectPathsResponse{}, err
	}
	workspaceDir, err := build.WorkspaceDir(appRoot, cfg.Name)
	if err != nil {
		return inspectPathsResponse{}, err
	}
	statePath, err := build.BuildStatePath(appRoot, cfg.Name)
	if err != nil {
		return inspectPathsResponse{}, err
	}
	resp := inspectPathsResponse{
		SchemaVersion: "onlava.inspect.paths.v1",
		App:           inspectAppInfo(appRoot, cfg, nil),
		Paths: inspectPathsRecord{
			AppRoot:        appRoot,
			ConfigPath:     filepath.Join(appRoot, ".onlava.json"),
			CacheRoot:      cacheRoot,
			BuildRoot:      filepath.Join(cacheRoot, "build"),
			WorkspaceDir:   workspaceDir,
			BinaryPath:     filepath.Join(workspaceDir, "onlava-app"),
			BuildStatePath: statePath,
		},
	}
	return resp, nil
}

func buildInspectTemporalResponse(ctx context.Context, appRoot string, cfg appcfg.Config) (inspectTemporalResponse, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	info, status := checkTemporalConnection(checkCtx, cfg.Name, temporalRuntimeConfigFromApp(cfg.Temporal))
	appModel, err := cachedInspectAppModel(appRoot, cfg.Name)
	if err != nil {
		return inspectTemporalResponse{}, err
	}
	ts := workers.DiscoverTypeScriptActivities(appRoot)
	tsDiagnostics := workers.ValidateTypeScriptContracts(ts, temporalExternalActivityDeclarations(appRoot, appModel), nativeGoTemporalDeclarations(appRoot, appModel))
	return inspectTemporalResponse{
		SchemaVersion: "onlava.inspect.temporal.v1",
		App:           inspectAppInfo(appRoot, cfg, appModel),
		Temporal: inspectTemporalRecord{
			Enabled:          info.Enabled,
			Mode:             info.Mode,
			Address:          info.Address,
			AddressEnv:       info.AddressEnv,
			AddressEnvSet:    info.AddressEnvSet,
			Namespace:        info.Namespace,
			NamespaceEnvSet:  info.NamespaceEnvSet,
			TaskQueuePrefix:  info.TaskQueuePrefix,
			TaskQueueEnv:     info.TaskQueueEnv,
			TaskQueueEnvSet:  info.TaskQueueEnvSet,
			PayloadCodec:     info.PayloadCodec,
			APIKeyEnv:        info.APIKeyEnv,
			APIKeyEnvSet:     info.APIKeyEnvSet,
			TLSEnabled:       info.TLSEnabled,
			TLSServerNameEnv: info.TLSServerNameEnv,
			TLSServerNameSet: info.TLSServerNameSet,
			TLSCACertFileEnv: info.TLSCACertFileEnv,
			TLSCACertFileSet: info.TLSCACertFileSet,
			TLSCertFileEnv:   info.TLSCertFileEnv,
			TLSCertFileSet:   info.TLSCertFileSet,
			TLSKeyFileEnv:    info.TLSKeyFileEnv,
			TLSKeyFileSet:    info.TLSKeyFileSet,
			HostReporting:    info.HostReporting,
			HostReportingEnv: info.HostReportingEnv,
			HostReportingSet: info.HostReportingSet,
			DeploymentName:   info.DeploymentName,
			DeploymentEnv:    info.DeploymentEnv,
			DeploymentEnvSet: info.DeploymentEnvSet,
			WorkerBuildID:    info.WorkerBuildID,
			WorkerBuildIDEnv: info.WorkerBuildIDEnv,
			WorkerBuildIDSet: info.WorkerBuildIDSet,
			Versioning:       info.Versioning,
			VersioningEnv:    info.VersioningEnv,
			VersioningEnvSet: info.VersioningEnvSet,
			LocalAutoStart:   info.LocalAutoStart,
			LocalDBFilename:  info.LocalDBFilename,
			ConnectTimeoutMS: info.ConnectTimeoutMS,
		},
		Declarations: temporalDeclarations(appRoot, appModel, info),
		TypeScript:   temporalTypeScriptResponse(appRoot, ts, tsDiagnostics),
		Connectivity: temporalConnectivity{
			Checked:   status.Checked,
			Reachable: status.Reachable,
			Error:     status.Error,
		},
		WorkerManifests: workers.ValidateWithKnownActivities(appRoot, cfg.Name, knownTemporalActivityNames(appModel)),
	}, nil
}

func temporalTypeScriptResponse(appRoot string, ts workers.TypeScriptWorkerModel, diagnostics []workers.Diagnostic) temporalTypeScript {
	activities := make([]temporalTypeScriptActivity, 0, len(ts.Activities))
	for _, activity := range ts.Activities {
		activities = append(activities, temporalTypeScriptActivity{
			Name:           activity.Name,
			TaskQueue:      activity.TaskQueue,
			ExportName:     activity.ExportName,
			Input:          activity.Input,
			Output:         activity.Output,
			File:           activity.File,
			Line:           activity.Line,
			MaxConcurrency: activity.MaxConcurrency,
		})
	}
	return temporalTypeScript{
		Checked:      true,
		OK:           len(diagnostics) == 0,
		GeneratedDir: filepath.ToSlash(filepath.Join(appRoot, workers.TypeScriptWorkerGeneratedRelDir)),
		Activities:   activities,
		Diagnostics:  diagnostics,
	}
}

func temporalDeclarations(appRoot string, appModel *model.App, info onlavaruntime.TemporalRuntimeInfo) []temporalDeclaration {
	if appModel == nil {
		return nil
	}
	out := make([]temporalDeclaration, 0, len(appModel.Runtime))
	for _, decl := range appModel.Runtime {
		if decl.Kind != model.RuntimeDeclarationTemporalWorkflow && decl.Kind != model.RuntimeDeclarationTemporalActivity && decl.Kind != model.RuntimeDeclarationTemporalExternalActivity {
			continue
		}
		queue := decl.TaskQueue
		explicit := decl.TaskQueueExplicit
		if decl.Kind == model.RuntimeDeclarationTemporalWorkflow && queue == "" && decl.TaskQueueResolved {
			queue = defaultTemporalWorkerTaskQueue(info.TaskQueuePrefix)
			explicit = false
		}
		queue = onlavaruntime.SessionScopedTemporalTaskQueue(info, queue)
		position := decl.Package.GoPkg.Fset.Position(decl.TokenPos)
		out = append(out, temporalDeclaration{
			Kind:              string(decl.Kind),
			Name:              decl.Name,
			TaskQueue:         queue,
			TaskQueueExplicit: explicit,
			File:              normalizeDiagnosticFile(appRoot, position.Filename),
			Line:              position.Line,
		})
	}
	return out
}

func defaultTemporalWorkerTaskQueue(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "onlava"
	}
	return strings.TrimSuffix(prefix, ".") + ".worker.go"
}

func knownTemporalActivityNames(appModel *model.App) []string {
	if appModel == nil {
		return nil
	}
	var names []string
	for _, decl := range appModel.Runtime {
		if (decl.Kind == model.RuntimeDeclarationTemporalActivity || decl.Kind == model.RuntimeDeclarationTemporalExternalActivity) && decl.Name != "" {
			names = append(names, decl.Name)
		}
	}
	return names
}

func knownTemporalActivityNamesFromRoot(appRoot, appName string) []string {
	appModel, err := cachedInspectAppModel(appRoot, appName)
	if err != nil {
		return nil
	}
	return knownTemporalActivityNames(appModel)
}

func inspectAppInfo(appRoot string, cfg appcfg.Config, app *model.App) inspectdata.AppRef {
	if app == nil {
		return inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".onlava.json"),
		}
	}
	return inspectdata.BuildAppResponse(appRoot, cfg, app).App
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
