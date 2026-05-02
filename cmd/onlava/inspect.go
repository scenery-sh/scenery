package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	appcfg "onlava.com/internal/app"
	"onlava.com/internal/build"
	inspectdata "onlava.com/internal/inspect"
	"onlava.com/internal/model"
	"onlava.com/internal/parse"
	"onlava.com/internal/wiremodel"
)

type inspectOptions struct {
	Subject  string
	AppRoot  string
	RepoRoot string
	JSON     bool
	Trace    inspectTraceQueryOptions
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
