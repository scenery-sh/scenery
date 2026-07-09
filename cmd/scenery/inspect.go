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
	"sort"
	"strings"
	"sync"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/postgresdb"
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

type inspectDurableResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	App           inspectdata.AppRef     `json:"app"`
	Durable       inspectDurableRecord   `json:"durable"`
	Declarations  []durableDeclaration   `json:"declarations"`
	Services      []durableServiceRecord `json:"services"`
}

type inspectDurableRecord struct {
	Database     inspectDurableDatabase `json:"database"`
	Schema       string                 `json:"schema"`
	TaskCount    int                    `json:"task_count"`
	ServiceCount int                    `json:"service_count"`
}

type inspectDurableDatabase struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

type durableDeclaration struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Service string `json:"service"`
	Schema  string `json:"schema"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Input   string `json:"input,omitempty"`
	Output  string `json:"output,omitempty"`
}

type durableServiceRecord struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type inspectStorageResponse struct {
	SchemaVersion string                `json:"schema_version"`
	App           inspectdata.AppRef    `json:"app"`
	Storage       inspectStorageRecord  `json:"storage"`
	Stores        []inspectStorageStore `json:"stores"`
}

type inspectStorageRecord struct {
	Configured bool                   `json:"configured"`
	CellID     string                 `json:"storage_cell_id,omitempty"`
	Share      string                 `json:"share,omitempty"`
	Default    string                 `json:"default,omitempty"`
	Readiness  string                 `json:"readiness"`
	Runtime    *inspectStorageRuntime `json:"runtime,omitempty"`
}

type inspectStorageStore struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Access         string `json:"access"`
	TenantScoped   bool   `json:"tenant_scoped"`
	MaxObjectBytes int64  `json:"max_object_bytes,omitempty"`
	ObjectCount    int    `json:"object_count"`
	TotalBytes     int64  `json:"total_bytes"`
}

type inspectStorageRuntime struct {
	CellRoot   string `json:"cell_root,omitempty"`
	ObjectsDir string `json:"objects_dir,omitempty"`
	Exists     bool   `json:"exists"`
}

func inspectCommand(args []string) error {
	return runSceneryInspect(args, os.Stdout)
}

func runSceneryInspect(args []string, stdout io.Writer) error {
	opts, err := parseInspectArgs(args)
	if err != nil {
		return err
	}
	if !opts.JSON {
		return fmt.Errorf("scenery inspect currently requires --json")
	}

	if opts.Subject == "docs" {
		repoRoot, err := discoverSceneryRepoRoot(opts.RepoRoot)
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
	case "models":
		if payload, ok, err := inspectdata.ReadGeneratedModels(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, inspectdata.BuildModelsResponse(appRoot, cfg, model))
	case "views":
		if payload, ok, err := inspectdata.ReadGeneratedViews(appRoot); err != nil {
			return err
		} else if ok {
			return writeInspectJSON(stdout, payload)
		}
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, inspectdata.BuildViewsResponse(appRoot, cfg, model))
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
	case "durable":
		model, err := cachedInspectAppModel(appRoot, cfg.Name)
		if err != nil {
			return err
		}
		return writeInspectJSON(stdout, buildInspectDurableResponse(appRoot, cfg, model))
	case "storage":
		return writeInspectJSON(stdout, buildInspectStorageResponse(context.Background(), appRoot, cfg))
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
		return inspectOptions{}, fmt.Errorf("unknown inspect subject %q; use `scenery %s list`", opts.Subject, opts.Subject)
	}
	flags := newCLIFlagSet("inspect " + opts.Subject)
	flags.BoolVar(&opts.JSON, "json", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.RepoRoot, "repo-root", "", "")
	flags.StringVar(&opts.Harness.Severity, "severity", "", "")
	flags.IntVar(&opts.Harness.Top, "top", 0, "")
	traceValues := map[string]*string{}
	for _, name := range []string{"limit", "since", "service", "endpoint", "trace-id", "session", "status", "min-duration-ms"} {
		value := new(string)
		traceValues[name] = value
		flags.StringVar(value, name, "", "")
	}
	flags.StringVar(traceValues["limit"], "n", "", "")
	flags.BoolVar(&opts.Trace.Slowest, "slowest", false, "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return inspectOptions{}, err
	}
	if cliFlagSet(flags, "repo-root") && opts.Subject != "docs" && opts.Subject != "harness" {
		return inspectOptions{}, fmt.Errorf("--repo-root is only supported for inspect docs and inspect harness")
	}
	if opts.Subject == "harness" && len(positionals) > 0 {
		opts.Harness.Topic = positionals[0]
		if opts.Harness.Topic != "artifact" && opts.Harness.Topic != "diagnostics" && opts.Harness.Topic != "timing" {
			return inspectOptions{}, fmt.Errorf("unknown flag %q", positionals[0])
		}
		positionals = positionals[1:]
		if opts.Harness.Topic == "artifact" {
			if len(positionals) == 0 {
				return inspectOptions{}, fmt.Errorf("missing inspect harness artifact name")
			}
			opts.Harness.Name, positionals = positionals[0], positionals[1:]
		}
	}
	if len(positionals) > 0 {
		return inspectOptions{}, fmt.Errorf("unknown flag %q", positionals[0])
	}
	if cliFlagSet(flags, "severity") && (opts.Subject != "harness" || opts.Harness.Topic != "diagnostics") {
		return inspectOptions{}, fmt.Errorf("--severity is only supported for inspect harness diagnostics")
	}
	if cliFlagSet(flags, "top") && (opts.Subject != "harness" || opts.Harness.Topic != "timing") {
		return inspectOptions{}, fmt.Errorf("--top is only supported for inspect harness timing")
	}
	if cliFlagSet(flags, "top") && opts.Harness.Top <= 0 {
		return inspectOptions{}, fmt.Errorf("--top must be a positive integer")
	}
	for _, name := range []string{"limit", "since", "service", "endpoint", "trace-id", "session", "status", "min-duration-ms"} {
		aliases := []string{name}
		if name == "limit" {
			aliases = append(aliases, "n")
		}
		if !cliFlagSet(flags, aliases...) {
			continue
		}
		if name == "session" && opts.Subject == "observability" {
			opts.Trace.Session = strings.TrimSpace(*traceValues[name])
			if opts.Trace.Session == "" {
				return inspectOptions{}, fmt.Errorf("invalid session %q", *traceValues[name])
			}
			continue
		}
		if opts.Subject != "traces" && opts.Subject != "metrics" {
			return inspectOptions{}, fmt.Errorf("--%s is only supported for traces list and metrics list", name)
		}
		if err := parseInspectTraceFlags(&opts, "--"+name, *traceValues[name]); err != nil {
			return inspectOptions{}, err
		}
	}
	if cliFlagSet(flags, "slowest") && opts.Subject != "traces" && opts.Subject != "metrics" {
		return inspectOptions{}, fmt.Errorf("--slowest is only supported for traces list and metrics list")
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
		if d.IsDir() {
			if appwalk.SkipDir(appRoot, path) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case appcfg.IsConfigFilename(rel), rel == "go.mod", rel == "go.sum", strings.HasSuffix(rel, ".go"):
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
			SchemaVersion: "scenery.inspect.build.v1",
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
	binaryPath := filepath.Join(workspaceDir, "scenery-app")
	resp := inspectBuildResponse{
		SchemaVersion: "scenery.inspect.build.v1",
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
		SchemaVersion: "scenery.inspect.paths.v1",
		App:           inspectAppInfo(appRoot, cfg, nil),
		Paths: inspectPathsRecord{
			AppRoot:        appRoot,
			ConfigPath:     cfg.SourcePath(appRoot),
			CacheRoot:      cacheRoot,
			BuildRoot:      filepath.Join(cacheRoot, "build"),
			WorkspaceDir:   workspaceDir,
			BinaryPath:     filepath.Join(workspaceDir, "scenery-app"),
			BuildStatePath: statePath,
		},
	}
	return resp, nil
}

func buildInspectStorageResponse(ctx context.Context, appRoot string, cfg appcfg.Config) inspectStorageResponse {
	_ = ctx
	storage := inspectStorageRecord{Configured: len(cfg.Storage.Stores) > 0, Readiness: "not_configured"}
	plan, planErr := resolveStorageCellPlan(cfg, "")
	if storage.Configured {
		storage.CellID = cfg.StorageCellID()
		storage.Share = firstNonEmpty(strings.TrimSpace(cfg.Storage.Share), "worktree")
		storage.Default = strings.TrimSpace(cfg.Storage.Default)
		storage.Readiness = "configured"
		if planErr == nil && plan != nil {
			runtime := &inspectStorageRuntime{CellRoot: filepath.ToSlash(plan.CellRoot), ObjectsDir: filepath.ToSlash(plan.ObjectsDir)}
			if info, err := os.Stat(plan.ObjectsDir); err == nil && info.IsDir() {
				runtime.Exists = true
				storage.Readiness = "ready"
			}
			storage.Runtime = runtime
		}
	}
	stores := make([]inspectStorageStore, 0, len(cfg.Storage.Stores))
	for name, store := range cfg.Storage.Stores {
		record := inspectStorageStore{
			Name:           name,
			Kind:           firstNonEmpty(strings.TrimSpace(store.Kind), "local"),
			Access:         firstNonEmpty(strings.TrimSpace(store.Access), "auth"),
			TenantScoped:   store.TenantScoped,
			MaxObjectBytes: store.MaxObjectBytes,
		}
		if planErr == nil && plan != nil {
			record.ObjectCount, record.TotalBytes = storageStoreUsage(plan.storageStoreObjectsDir(name))
		}
		stores = append(stores, record)
	}
	sort.Slice(stores, func(i, j int) bool { return stores[i].Name < stores[j].Name })

	return inspectStorageResponse{
		SchemaVersion: "scenery.storage.inspect.v1",
		App:           inspectAppInfo(appRoot, cfg, nil),
		Storage:       storage,
		Stores:        stores,
	}
}

// storageStoreUsage counts the object files under a store's on-disk root,
// excluding Scenery-owned sidecar metadata. Missing directories report zero.
func storageStoreUsage(root string) (int, int64) {
	if strings.TrimSpace(root) == "" {
		return 0, 0
	}
	var count int
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if strings.HasPrefix(filepath.ToSlash(rel), "__scenery/") {
			return nil
		}
		count++
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return count, total
}

func buildInspectDurableResponse(appRoot string, cfg appcfg.Config, appModel *model.App) inspectDurableResponse {
	databaseURL := durableDatabaseURLForInspect(appRoot, cfg)
	declarations := durableDeclarations(appRoot, cfg, appModel)
	services := durableServices(declarations)
	return inspectDurableResponse{
		SchemaVersion: "scenery.inspect.durable.v2",
		App:           inspectAppInfo(appRoot, cfg, appModel),
		Durable: inspectDurableRecord{
			Database: inspectDurableDatabase{
				Name: postgresdb.DatabaseNameFromURL(databaseURL),
				URL:  postgresdb.RedactURL(databaseURL),
			},
			Schema:       "scenery",
			TaskCount:    len(declarations),
			ServiceCount: len(services),
		},
		Declarations: declarations,
		Services:     services,
	}
}

func durableDeclarations(appRoot string, cfg appcfg.Config, appModel *model.App) []durableDeclaration {
	if appModel == nil {
		return nil
	}
	out := make([]durableDeclaration, 0, len(appModel.Runtime))
	for _, decl := range appModel.Runtime {
		if decl.Kind != model.RuntimeDeclarationDurableTask {
			continue
		}
		position := decl.Package.Analysis.Fset.Position(decl.TokenPos)
		schema := decl.ServiceName
		if svc, ok := cfg.DatabaseService(decl.ServiceName); ok {
			schema = svc.Schema
		}
		out = append(out, durableDeclaration{
			Kind:    string(decl.Kind),
			Name:    decl.Name,
			Service: decl.ServiceName,
			Schema:  schema,
			File:    normalizeDiagnosticFile(appRoot, position.Filename),
			Line:    position.Line,
			Input:   decl.InputType,
			Output:  decl.OutputType,
		})
	}
	return out
}

func durableServices(declarations []durableDeclaration) []durableServiceRecord {
	byName := make(map[string]durableServiceRecord)
	for _, decl := range declarations {
		if strings.TrimSpace(decl.Service) == "" {
			continue
		}
		byName[decl.Service] = durableServiceRecord{
			Name:   decl.Service,
			Schema: decl.Schema,
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]durableServiceRecord, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

func durableDatabaseURLForInspect(appRoot string, cfg appcfg.Config) string {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		env = envpolicy.Environ()
	}
	if value, _ := lookupEnvValue(env, cfg.DatabaseURLEnv()); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, _ := lookupEnvValue(env, postgresdb.RegistryEnv); strings.TrimSpace(value) != "" {
		registry, err := postgresdb.DecodeRegistry(value)
		if err == nil {
			return strings.TrimSpace(registry.URL)
		}
	}
	return ""
}

func inspectAppInfo(appRoot string, cfg appcfg.Config, app *model.App) inspectdata.AppRef {
	if app == nil {
		return inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: cfg.SourcePath(appRoot),
		}
	}
	return inspectdata.BuildAppResponse(appRoot, cfg, app).App
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
