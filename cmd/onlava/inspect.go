package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	datainspect "github.com/pbrazdil/onlava/internal/datainspect"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
	"github.com/pbrazdil/onlava/internal/wiremodel"
	"github.com/pbrazdil/onlava/internal/workers"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

type inspectOptions struct {
	Subject  string
	AppRoot  string
	RepoRoot string
	JSON     bool
	Trace    inspectTraceQueryOptions
	Data     datainspect.Options
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

	if opts.Subject == "data" {
		resp, err := datainspect.Build(context.Background(), opts.Data)
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
		model, err := parse.App(appRoot, cfg.Name)
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
		model, err := parse.App(appRoot, cfg.Name)
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
		model, err := parse.App(appRoot, cfg.Name)
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
		model, err := parse.App(appRoot, cfg.Name)
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
		model, err := parse.App(appRoot, cfg.Name)
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
	case "temporal":
		resp, err := buildInspectTemporalResponse(context.Background(), appRoot, cfg)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "traces":
		resp, err := buildInspectTracesResponse(context.Background(), appRoot, cfg, opts.Trace)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	case "metrics":
		resp, err := buildInspectMetricsResponse(context.Background(), appRoot, cfg, opts.Trace)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, resp)
	default:
		return fmt.Errorf("unknown inspect subject %q", opts.Subject)
	}
}

func parseInspectArgs(args []string) (inspectOptions, error) {
	if len(args) == 0 {
		return inspectOptions{}, fmt.Errorf("missing inspect subject")
	}
	opts := inspectOptions{Subject: args[0]}
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
			if opts.Subject != "docs" {
				return inspectOptions{}, fmt.Errorf("--repo-root is only supported for inspect docs")
			}
			opts.RepoRoot = args[i]
		case "--limit", "-n", "--since", "--service", "--endpoint", "--trace-id", "--status", "--min-duration-ms":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for %s", args[i-1])
			}
			if opts.Subject != "traces" && opts.Subject != "metrics" {
				return inspectOptions{}, fmt.Errorf("%s is only supported for inspect traces and metrics", args[i-1])
			}
			if err := parseInspectTraceFlags(&opts, args[i-1], args[i]); err != nil {
				return inspectOptions{}, err
			}
		case "--database-url":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --database-url")
			}
			if opts.Subject != "data" {
				return inspectOptions{}, fmt.Errorf("--database-url is only supported for inspect data")
			}
			opts.Data.DatabaseURL = args[i]
		case "--tenant":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --tenant")
			}
			if opts.Subject != "data" {
				return inspectOptions{}, fmt.Errorf("--tenant is only supported for inspect data")
			}
			opts.Data.TenantKey = args[i]
		case "--object":
			i++
			if i >= len(args) {
				return inspectOptions{}, fmt.Errorf("missing value for --object")
			}
			if opts.Subject != "data" {
				return inspectOptions{}, fmt.Errorf("--object is only supported for inspect data")
			}
			opts.Data.ObjectName = args[i]
		case "--slowest":
			if opts.Subject != "traces" && opts.Subject != "metrics" {
				return inspectOptions{}, fmt.Errorf("%s is only supported for inspect traces and metrics", args[i])
			}
			opts.Trace.Slowest = true
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
	info, status := onlavaruntime.CheckTemporalConnection(checkCtx, cfg.Name, temporalRuntimeConfigFromApp(cfg.Temporal))
	appModel, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return inspectTemporalResponse{}, err
	}
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
		Connectivity: temporalConnectivity{
			Checked:   status.Checked,
			Reachable: status.Reachable,
			Error:     status.Error,
		},
		WorkerManifests: workers.ValidateWithKnownActivities(appRoot, cfg.Name, knownTemporalActivityNames(appModel)),
	}, nil
}

func temporalDeclarations(appRoot string, appModel *model.App, info onlavaruntime.TemporalRuntimeInfo) []temporalDeclaration {
	if appModel == nil {
		return nil
	}
	out := make([]temporalDeclaration, 0, len(appModel.Runtime))
	for _, decl := range appModel.Runtime {
		if decl.Kind != model.RuntimeDeclarationTemporalWorkflow && decl.Kind != model.RuntimeDeclarationTemporalActivity {
			continue
		}
		queue := decl.TaskQueue
		explicit := decl.TaskQueueExplicit
		if decl.Kind == model.RuntimeDeclarationTemporalWorkflow && queue == "" && decl.TaskQueueResolved {
			queue = defaultTemporalWorkerTaskQueue(info.TaskQueuePrefix)
			explicit = false
		}
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
		if decl.Kind == model.RuntimeDeclarationTemporalActivity && decl.Name != "" {
			names = append(names, decl.Name)
		}
	}
	return names
}

func knownTemporalActivityNamesFromRoot(appRoot, appName string) []string {
	appModel, err := parse.App(appRoot, appName)
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
