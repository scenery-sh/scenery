package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/build"
)

const (
	doctorSchemaVersion         = "scenery.doctor.result.v1"
	doctorCommandTimeout        = 2 * time.Second
	doctorMinGoMajor            = 1
	doctorMinGoMinor            = 26
	doctorDiskWarnBytes         = 5 * 1024 * 1024 * 1024
	doctorDiskErrorBytes        = 1 * 1024 * 1024 * 1024
	doctorSizeWalkTimeout       = 2 * time.Second
	doctorMemoryWarnBytes       = 4 * 1024 * 1024 * 1024
	doctorMemoryErrorBytes      = 2 * 1024 * 1024 * 1024
	doctorStatusOK              = "ok"
	doctorStatusWarn            = "warn"
	doctorStatusError           = "error"
	doctorStatusSkipped         = "skipped"
	doctorSeverityRequired      = "required"
	doctorSeverityOptional      = "optional"
	doctorSeverityInformational = "informational"
)

type doctorOptions struct {
	AppRoot string
	JSON    bool
}

type doctorResponse struct {
	SchemaVersion string            `json:"schema_version"`
	OK            bool              `json:"ok"`
	Summary       doctorSummary     `json:"summary"`
	Scenery       versionResponse   `json:"scenery"`
	App           *doctorAppInfo    `json:"app,omitempty"`
	Environment   doctorEnvironment `json:"environment"`
	Checks        []doctorCheck     `json:"checks"`
}

type doctorSummary struct {
	OK       int `json:"ok"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
	Skipped  int `json:"skipped"`
}

type doctorAppInfo struct {
	Root       string `json:"root"`
	ConfigPath string `json:"config_path"`
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
}

type doctorEnvironment struct {
	GOOS             string             `json:"goos"`
	GOARCH           string             `json:"goarch"`
	NumCPU           int                `json:"num_cpu"`
	TotalMemoryBytes uint64             `json:"total_memory_bytes,omitempty"`
	Paths            []doctorPathReport `json:"paths"`
}

type doctorPathReport struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	TotalBytes uint64 `json:"total_bytes,omitempty"`
}

type doctorCheck struct {
	ID              string         `json:"id"`
	Category        string         `json:"category"`
	Name            string         `json:"name"`
	Status          string         `json:"status"`
	Severity        string         `json:"severity"`
	Message         string         `json:"message"`
	SuggestedAction string         `json:"suggested_action,omitempty"`
	Observed        map[string]any `json:"observed,omitempty"`
}

type doctorProbeDeps struct {
	LookPath      func(file string) (string, error)
	RunCommand    func(ctx context.Context, name string, args ...string) ([]byte, error)
	ResourceProbe doctorResourceProbe
	Getwd         func() (string, error)
	CacheRoot     func() (string, error)
	AgentHome     func() (string, error)
	DiscoverApp   func(start string) (doctorAppInfo, appcfg.Config, bool, error)
}

type doctorResourceProbe interface {
	Runtime() doctorRuntimeInfo
	Memory(ctx context.Context) (doctorMemoryInfo, error)
	Disk(ctx context.Context, path string) (doctorDiskInfo, error)
}

type doctorRuntimeInfo struct {
	GOOS   string
	GOARCH string
	NumCPU int
}

type doctorMemoryInfo struct {
	TotalBytes uint64
}

type doctorDiskInfo struct {
	Path       string
	FreeBytes  uint64
	TotalBytes uint64
}

type doctorPathSizeInfo struct {
	Path      string
	SizeBytes uint64
	FileCount int
	DirCount  int
}

type doctorAppFeatures struct {
	SQLCConfigured       bool
	AtlasRelevant        bool
	FrontendConfigured   bool
	TypeScriptTemporal   bool
	TypeScriptTasks      bool
	DockerRelevant       bool
	DatabaseApplyCommand bool
}

func doctorCommand(args []string) error {
	return runSceneryDoctor(context.Background(), os.Stdout, args)
}

func runSceneryDoctor(ctx context.Context, stdout io.Writer, args []string) error {
	return runSceneryDoctorWithDeps(ctx, stdout, args, defaultDoctorProbeDeps())
}

func runSceneryDoctorWithDeps(ctx context.Context, stdout io.Writer, args []string, deps doctorProbeDeps) error {
	opts, err := parseDoctorArgs(args)
	if err != nil {
		return err
	}
	resp := buildDoctorResponse(ctx, opts, deps)
	if opts.JSON {
		if err := writeDoctorJSON(stdout, resp); err != nil {
			return err
		}
		if !resp.OK {
			return &silentCLIError{err: fmt.Errorf("scenery doctor found %d error(s)", resp.Summary.Errors)}
		}
		return nil
	}
	if err := writeDoctorText(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("scenery doctor found %d error(s)", resp.Summary.Errors)
	}
	return nil
}

func parseDoctorArgs(args []string) (doctorOptions, error) {
	opts := doctorOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return doctorOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			return doctorOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func defaultDoctorProbeDeps() doctorProbeDeps {
	return doctorProbeDeps{
		LookPath:      exec.LookPath,
		RunCommand:    doctorRunCommand,
		ResourceProbe: defaultDoctorResourceProbe{},
		Getwd:         os.Getwd,
		CacheRoot:     build.CacheRoot,
		AgentHome: func() (string, error) {
			paths, err := localagent.DefaultPaths()
			if err != nil {
				return "", err
			}
			return paths.Home, nil
		},
		DiscoverApp: func(start string) (doctorAppInfo, appcfg.Config, bool, error) {
			root, cfg, err := appcfg.DiscoverRoot(start)
			if err != nil {
				return doctorAppInfo{}, appcfg.Config{}, false, err
			}
			return doctorAppInfo{
				Root:       root,
				ConfigPath: filepath.Join(root, ".scenery.json"),
				Name:       cfg.Name,
				ID:         cfg.ID,
			}, cfg, true, nil
		},
	}
}

func doctorRunCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return bytes.TrimSpace(out.Bytes()), err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func buildDoctorResponse(ctx context.Context, opts doctorOptions, deps doctorProbeDeps) doctorResponse {
	deps = fillDoctorProbeDeps(deps)
	resp := doctorResponse{
		SchemaVersion: doctorSchemaVersion,
		OK:            true,
		Scenery:       buildVersionResponse(),
	}

	runtimeInfo := deps.ResourceProbe.Runtime()
	resp.Environment.GOOS = runtimeInfo.GOOS
	resp.Environment.GOARCH = runtimeInfo.GOARCH
	resp.Environment.NumCPU = runtimeInfo.NumCPU

	resp.Checks = append(resp.Checks, doctorRuntimeCheck(runtimeInfo))
	resp.Checks = append(resp.Checks, doctorCPUCheck(runtimeInfo.NumCPU))
	if memory, err := deps.ResourceProbe.Memory(ctx); err != nil {
		resp.Checks = append(resp.Checks, doctorCheck{
			ID:              "resource.memory",
			Category:        "resource",
			Name:            "System memory",
			Status:          doctorStatusSkipped,
			Severity:        doctorSeverityInformational,
			Message:         "total physical memory could not be determined: " + err.Error(),
			SuggestedAction: "Continue if other checks are healthy, or verify machine memory manually.",
		})
	} else {
		resp.Environment.TotalMemoryBytes = memory.TotalBytes
		resp.Checks = append(resp.Checks, doctorMemoryCheck(memory))
	}

	var cfg appcfg.Config
	var appFound bool
	appStart := opts.AppRoot
	if strings.TrimSpace(appStart) == "" {
		if cwd, err := deps.Getwd(); err == nil {
			appStart = cwd
		} else {
			resp.Checks = append(resp.Checks, doctorCheck{
				ID:              "path.cwd",
				Category:        "path",
				Name:            "Current directory",
				Status:          doctorStatusWarn,
				Severity:        doctorSeverityOptional,
				Message:         "current directory could not be resolved: " + err.Error(),
				SuggestedAction: "Run `scenery doctor` from a readable directory.",
			})
		}
	}
	if strings.TrimSpace(appStart) != "" {
		app, discoveredCfg, ok, err := deps.DiscoverApp(appStart)
		if err != nil {
			if strings.TrimSpace(opts.AppRoot) != "" {
				resp.Checks = append(resp.Checks, doctorCheck{
					ID:              "app.root",
					Category:        "app",
					Name:            "App root",
					Status:          doctorStatusError,
					Severity:        doctorSeverityRequired,
					Message:         "app root could not be discovered from " + appStart + ": " + err.Error(),
					SuggestedAction: "Pass a directory inside an app that contains `.scenery.json`.",
				})
			}
		} else if ok {
			resp.App = &app
			cfg = discoveredCfg
			appFound = true
			resp.Checks = append(resp.Checks, doctorCheck{
				ID:       "app.root",
				Category: "app",
				Name:     "App root",
				Status:   doctorStatusOK,
				Severity: doctorSeverityInformational,
				Message:  fmt.Sprintf("%s at %s", app.Name, app.Root),
				Observed: map[string]any{
					"root":        app.Root,
					"config_path": app.ConfigPath,
					"name":        app.Name,
					"id":          app.ID,
				},
			})
		}
	}

	diskPaths := doctorDiskPaths(opts, resp.App, deps)
	for _, path := range diskPaths {
		resp.Checks = append(resp.Checks, doctorDiskCheck(ctx, deps.ResourceProbe, path, &resp.Environment)...)
	}
	resp.Checks = append(resp.Checks, doctorStorageSizeChecks(ctx, deps)...)

	features := doctorFeatures(cfg, resp.App)
	resp.Checks = append(resp.Checks, doctorDependencyChecks(ctx, deps, features, appFound)...)
	resp.Checks = append(resp.Checks, doctorDockerChecks(ctx, deps)...)

	resp.Summary = summarizeDoctorChecks(resp.Checks)
	resp.OK = resp.Summary.Errors == 0
	return resp
}

func fillDoctorProbeDeps(deps doctorProbeDeps) doctorProbeDeps {
	defaults := defaultDoctorProbeDeps()
	if deps.LookPath == nil {
		deps.LookPath = defaults.LookPath
	}
	if deps.RunCommand == nil {
		deps.RunCommand = defaults.RunCommand
	}
	if deps.ResourceProbe == nil {
		deps.ResourceProbe = defaults.ResourceProbe
	}
	if deps.Getwd == nil {
		deps.Getwd = defaults.Getwd
	}
	if deps.CacheRoot == nil {
		deps.CacheRoot = defaults.CacheRoot
	}
	if deps.AgentHome == nil {
		deps.AgentHome = defaults.AgentHome
	}
	if deps.DiscoverApp == nil {
		deps.DiscoverApp = defaults.DiscoverApp
	}
	return deps
}

func doctorRuntimeCheck(info doctorRuntimeInfo) doctorCheck {
	status := doctorStatusOK
	severity := doctorSeverityInformational
	message := info.GOOS + "/" + info.GOARCH
	action := ""
	if !doctorSupportedRuntime(info.GOOS) {
		status = doctorStatusWarn
		severity = doctorSeverityOptional
		message += " is not a routinely tested scenery development platform"
		action = "Prefer linux, darwin, or windows for local development when possible."
	}
	return doctorCheck{
		ID:              "os.runtime",
		Category:        "host",
		Name:            "Operating system",
		Status:          status,
		Severity:        severity,
		Message:         message,
		SuggestedAction: action,
		Observed: map[string]any{
			"goos":   info.GOOS,
			"goarch": info.GOARCH,
		},
	}
}

func doctorSupportedRuntime(goos string) bool {
	switch goos {
	case "linux", "darwin", "windows":
		return true
	default:
		return false
	}
}

func doctorCPUCheck(numCPU int) doctorCheck {
	check := doctorCheck{
		ID:       "resource.cpu",
		Category: "resource",
		Name:     "CPU",
		Status:   doctorStatusOK,
		Severity: doctorSeverityInformational,
		Message:  fmt.Sprintf("%d logical CPUs", numCPU),
		Observed: map[string]any{"num_cpu": numCPU},
	}
	if numCPU < 2 {
		check.Status = doctorStatusWarn
		check.Severity = doctorSeverityOptional
		check.Message = fmt.Sprintf("%d logical CPU; local dev may be slow", numCPU)
		check.SuggestedAction = "Use at least 2 logical CPUs for smoother scenery development."
	}
	return check
}

func doctorMemoryCheck(memory doctorMemoryInfo) doctorCheck {
	check := doctorCheck{
		ID:       "resource.memory",
		Category: "resource",
		Name:     "System memory",
		Status:   doctorStatusOK,
		Severity: doctorSeverityInformational,
		Message:  fmt.Sprintf("%s total memory", humanBytes(memory.TotalBytes)),
		Observed: map[string]any{"total_bytes": memory.TotalBytes},
	}
	switch {
	case memory.TotalBytes < doctorMemoryErrorBytes:
		check.Status = doctorStatusError
		check.Severity = doctorSeverityRequired
		check.Message = fmt.Sprintf("%s total memory; below the %s minimum", humanBytes(memory.TotalBytes), humanBytes(doctorMemoryErrorBytes))
		check.SuggestedAction = "Use a machine or container with more memory for scenery development."
	case memory.TotalBytes < doctorMemoryWarnBytes:
		check.Status = doctorStatusWarn
		check.Severity = doctorSeverityOptional
		check.Message = fmt.Sprintf("%s total memory; local dev may be slow", humanBytes(memory.TotalBytes))
		check.SuggestedAction = "Use at least 4 GiB RAM for smoother scenery development."
	}
	return check
}

func doctorDiskPaths(opts doctorOptions, app *doctorAppInfo, deps doctorProbeDeps) []doctorPathReport {
	seen := map[string]bool{}
	var out []doctorPathReport
	add := func(kind, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		key := kind + "\x00" + path
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, doctorPathReport{Kind: kind, Path: path})
	}
	if app != nil {
		add("app_root", app.Root)
	} else if strings.TrimSpace(opts.AppRoot) != "" {
		add("app_root", opts.AppRoot)
	} else if cwd, err := deps.Getwd(); err == nil {
		add("cwd", cwd)
	}
	if cacheRoot, err := deps.CacheRoot(); err == nil {
		add("cache_root", cacheRoot)
	}
	return out
}

func doctorDiskCheck(ctx context.Context, probe doctorResourceProbe, path doctorPathReport, env *doctorEnvironment) []doctorCheck {
	disk, err := probe.Disk(ctx, path.Path)
	if err != nil {
		return []doctorCheck{{
			ID:              "resource.disk." + path.Kind,
			Category:        "resource",
			Name:            "Disk space (" + path.Kind + ")",
			Status:          doctorStatusSkipped,
			Severity:        doctorSeverityInformational,
			Message:         "disk space could not be determined for " + path.Path + ": " + err.Error(),
			SuggestedAction: "Verify free disk space manually if local builds or caches fail.",
			Observed:        map[string]any{"path": path.Path, "kind": path.Kind},
		}}
	}
	report := doctorPathReport{
		Kind:       path.Kind,
		Path:       firstNonEmpty(disk.Path, path.Path),
		FreeBytes:  disk.FreeBytes,
		TotalBytes: disk.TotalBytes,
	}
	env.Paths = append(env.Paths, report)
	check := doctorCheck{
		ID:       "resource.disk." + path.Kind,
		Category: "resource",
		Name:     "Disk space (" + path.Kind + ")",
		Status:   doctorStatusOK,
		Severity: doctorSeverityInformational,
		Message:  fmt.Sprintf("%s free at %s", humanBytes(disk.FreeBytes), report.Path),
		Observed: map[string]any{
			"path":        report.Path,
			"kind":        path.Kind,
			"free_bytes":  disk.FreeBytes,
			"total_bytes": disk.TotalBytes,
		},
	}
	switch {
	case disk.FreeBytes < doctorDiskErrorBytes:
		check.Status = doctorStatusError
		check.Severity = doctorSeverityRequired
		check.Message = fmt.Sprintf("%s free at %s; below the %s minimum", humanBytes(disk.FreeBytes), report.Path, humanBytes(doctorDiskErrorBytes))
		check.SuggestedAction = "Free disk space before running builds, dev services, or managed tool downloads."
	case disk.FreeBytes < doctorDiskWarnBytes:
		check.Status = doctorStatusWarn
		check.Severity = doctorSeverityOptional
		check.Message = fmt.Sprintf("%s free at %s; local builds and caches may run out of space", humanBytes(disk.FreeBytes), report.Path)
		check.SuggestedAction = "Keep at least 5 GiB free for scenery build workspaces, caches, and local dev state."
	}
	return []doctorCheck{check}
}

func doctorStorageSizeChecks(ctx context.Context, deps doctorProbeDeps) []doctorCheck {
	home, err := deps.AgentHome()
	if err != nil {
		return []doctorCheck{{
			ID:              "storage.scenery_home",
			Category:        "storage",
			Name:            "Scenery home size",
			Status:          doctorStatusSkipped,
			Severity:        doctorSeverityInformational,
			Message:         "Scenery home size could not be determined: " + err.Error(),
			SuggestedAction: "Verify `SCENERY_AGENT_HOME` or the current user's home directory if local state inspection fails.",
		}}
	}
	home = filepath.Clean(home)
	return []doctorCheck{
		doctorPathSizeCheck(ctx, "storage.scenery_home", "Scenery home size", home, "Scenery home"),
		doctorPathSizeCheck(ctx, "storage.postgres_database", "Postgres database storage size", filepath.Join(home, "agent", "postgres"), "Postgres database storage"),
	}
}

func doctorPathSizeCheck(ctx context.Context, id, name, path, label string) doctorCheck {
	check := doctorCheck{
		ID:       id,
		Category: "storage",
		Name:     name,
		Status:   doctorStatusOK,
		Severity: doctorSeverityInformational,
		Observed: map[string]any{"path": path},
	}
	sizeCtx, cancel := context.WithTimeout(ctx, doctorSizeWalkTimeout)
	usage, err := doctorPathSize(sizeCtx, path)
	cancel()
	if errors.Is(err, os.ErrNotExist) {
		check.Status = doctorStatusSkipped
		check.Message = label + " is not present at " + path
		return check
	}
	if err != nil {
		check.Status = doctorStatusSkipped
		check.Message = label + " size could not be determined for " + path + ": " + err.Error()
		check.SuggestedAction = "Inspect the path manually if local state appears unexpectedly large."
		return check
	}
	check.Message = fmt.Sprintf("%s at %s", humanBytes(usage.SizeBytes), usage.Path)
	check.Observed["path"] = usage.Path
	check.Observed["size_bytes"] = usage.SizeBytes
	check.Observed["file_count"] = usage.FileCount
	check.Observed["dir_count"] = usage.DirCount
	return check
}

func doctorPathSize(ctx context.Context, path string) (doctorPathSizeInfo, error) {
	path = filepath.Clean(path)
	if err := ctx.Err(); err != nil {
		return doctorPathSizeInfo{}, err
	}
	rootInfo, err := os.Lstat(path)
	if err != nil {
		return doctorPathSizeInfo{}, err
	}
	usage := doctorPathSizeInfo{Path: path}
	if !rootInfo.IsDir() {
		if rootInfo.Size() > 0 {
			usage.SizeBytes = uint64(rootInfo.Size())
		}
		usage.FileCount = 1
		return usage, nil
	}
	err = filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			usage.DirCount++
		} else {
			usage.FileCount++
			if info.Size() > 0 {
				usage.SizeBytes += uint64(info.Size())
			}
		}
		return nil
	})
	return usage, err
}

func doctorFeatures(cfg appcfg.Config, app *doctorAppInfo) doctorAppFeatures {
	if app == nil {
		return doctorAppFeatures{}
	}
	features := doctorAppFeatures{}
	features.FrontendConfigured = len(cfg.Proxy.Frontends) > 0
	features.TypeScriptTemporal = cfg.Temporal.Enabled && cfg.Temporal.TypeScript.Enabled
	features.SQLCConfigured = sqlcGeneratorConfigured(cfg.Generators.SQLC)
	features.AtlasRelevant = sqlcUsesAtlas(cfg.Generators.SQLC)
	features.DatabaseApplyCommand = strings.TrimSpace(cfg.Database.Apply.Command) != ""
	features.DockerRelevant = appUsesDocker(cfg)
	features.TypeScriptTasks = appHasTypeScriptTasks(app.Root)
	return features
}

func sqlcGeneratorConfigured(cfg appcfg.SQLCGeneratorConfig) bool {
	return strings.TrimSpace(cfg.Provider) != "" ||
		strings.TrimSpace(cfg.Config) != "" ||
		strings.TrimSpace(cfg.DevURL) != "" ||
		len(cfg.Schemas) > 0
}

func sqlcUsesAtlas(cfg appcfg.SQLCGeneratorConfig) bool {
	for _, schema := range cfg.Schemas {
		if strings.TrimSpace(schema.AtlasSource) != "" || strings.TrimSpace(schema.AtlasSchema) != "" || strings.TrimSpace(schema.AtlasDevURL) != "" {
			return true
		}
	}
	return false
}

func appUsesDocker(cfg appcfg.Config) bool {
	for _, service := range cfg.Dev.Services {
		if strings.Contains(strings.ToLower(service.Kind), "docker") || strings.TrimSpace(service.Image) != "" {
			return true
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Generators.SQLC.DevURL)), "docker://") {
		return true
	}
	for _, schema := range cfg.Generators.SQLC.Schemas {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(schema.AtlasDevURL)), "docker://") {
			return true
		}
	}
	return false
}

func appHasTypeScriptTasks(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if appwalk.SkipDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".task.ts") || name == "index.ts" && strings.Contains(filepath.ToSlash(path), "/tasks/") {
			found = true
		}
		return nil
	})
	return found
}

type doctorToolSpec struct {
	ID              string
	Name            string
	Command         string
	VersionArgs     []string
	Required        bool
	Relevant        bool
	MissingMessage  string
	FoundMessage    string
	SuggestedAction string
}

func doctorDependencyChecks(ctx context.Context, deps doctorProbeDeps, features doctorAppFeatures, appFound bool) []doctorCheck {
	specs := []doctorToolSpec{
		{
			ID:              "tool.go",
			Name:            "Go toolchain",
			Command:         "go",
			VersionArgs:     []string{"version"},
			Required:        true,
			Relevant:        true,
			MissingMessage:  "go not found; scenery requires Go 1.26 or newer",
			SuggestedAction: "Install Go 1.26 or newer and ensure `go` is on PATH.",
		},
		{
			ID:              "tool.bun",
			Name:            "Bun",
			Command:         "bun",
			VersionArgs:     []string{"--version"},
			Relevant:        true,
			MissingMessage:  bunMissingMessage(features, appFound),
			SuggestedAction: "Install Bun when working on dashboard UI, managed frontends, benchmarks, or TypeScript workers.",
		},
		{
			ID:              "tool.atlas",
			Name:            "Atlas",
			Command:         "atlas",
			VersionArgs:     []string{"version"},
			Relevant:        appFound && features.AtlasRelevant,
			MissingMessage:  "atlas not found; configured SQLC schema refresh uses Atlas source files",
			SuggestedAction: "Install Atlas if you need `scenery generate sqlc` to refresh schema SQL from Atlas definitions.",
		},
		{
			ID:              "tool.sqlc",
			Name:            "SQLC",
			Command:         "sqlc",
			VersionArgs:     []string{"version"},
			Relevant:        appFound && features.SQLCConfigured,
			MissingMessage:  "sqlc not found; configured SQLC generation requires it",
			SuggestedAction: "Install sqlc if you need `scenery generate sqlc`.",
		},
		{
			ID:              "tool.git",
			Name:            "Git",
			Command:         "git",
			VersionArgs:     []string{"--version"},
			Relevant:        true,
			MissingMessage:  "git not found; useful for source checkouts and release/debug metadata",
			SuggestedAction: "Install Git for normal source-control workflows.",
		},
	}
	checks := make([]doctorCheck, 0, len(specs))
	for _, spec := range specs {
		if !spec.Relevant {
			continue
		}
		checks = append(checks, doctorToolCheck(ctx, deps, spec))
	}
	return checks
}

func bunMissingMessage(features doctorAppFeatures, appFound bool) string {
	var uses []string
	if features.FrontendConfigured {
		uses = append(uses, "managed frontends")
	}
	if features.TypeScriptTemporal {
		uses = append(uses, "TypeScript Temporal workers")
	}
	if features.TypeScriptTasks {
		uses = append(uses, "TypeScript code tasks")
	}
	if len(uses) == 0 {
		if appFound {
			return "bun not found; only needed for dashboard UI, benchmarks, TypeScript workers, or TypeScript code tasks"
		}
		return "bun not found; optional unless you work on dashboard UI, benchmarks, TypeScript workers, or TypeScript code tasks"
	}
	return "bun not found; this app may need it for " + strings.Join(uses, ", ")
}

func doctorToolCheck(ctx context.Context, deps doctorProbeDeps, spec doctorToolSpec) doctorCheck {
	path, err := deps.LookPath(spec.Command)
	if err != nil {
		status := doctorStatusWarn
		severity := doctorSeverityOptional
		if spec.Required {
			status = doctorStatusError
			severity = doctorSeverityRequired
		}
		return doctorCheck{
			ID:              spec.ID,
			Category:        "dependency",
			Name:            spec.Name,
			Status:          status,
			Severity:        severity,
			Message:         spec.MissingMessage,
			SuggestedAction: spec.SuggestedAction,
			Observed:        map[string]any{"command": spec.Command},
		}
	}
	check := doctorCheck{
		ID:       spec.ID,
		Category: "dependency",
		Name:     spec.Name,
		Status:   doctorStatusOK,
		Severity: doctorSeverityOptional,
		Message:  spec.Name + " found at " + path,
		Observed: map[string]any{
			"command": spec.Command,
			"path":    path,
		},
	}
	if spec.Required {
		check.Severity = doctorSeverityRequired
	}
	if len(spec.VersionArgs) > 0 {
		cmdCtx, cancel := context.WithTimeout(ctx, doctorCommandTimeout)
		out, err := deps.RunCommand(cmdCtx, path, spec.VersionArgs...)
		cancel()
		version := strings.TrimSpace(string(out))
		if version != "" {
			check.Observed["version"] = version
		}
		if err != nil {
			check.Status = doctorStatusWarn
			if spec.Required {
				check.Status = doctorStatusError
			}
			check.Message = spec.Name + " was found, but version probing failed"
			check.SuggestedAction = "Run `" + spec.Command + " " + strings.Join(spec.VersionArgs, " ") + "` manually and fix the command if it fails."
			return check
		}
		if version != "" {
			check.Message = version + " at " + path
		}
		if spec.ID == "tool.go" {
			versionInfo, ok := parseGoToolchainVersion(version)
			if !ok {
				check.Status = doctorStatusError
				check.Message = "could not parse Go version output: " + version
				check.SuggestedAction = "Install Go 1.26 or newer and ensure `go version` reports a standard version."
			} else {
				check.Observed["parsed_version"] = versionInfo.String()
				if versionInfo.compare(doctorGoVersion{Major: doctorMinGoMajor, Minor: doctorMinGoMinor}) < 0 {
					check.Status = doctorStatusError
					check.Message = fmt.Sprintf("%s found at %s; scenery requires Go %d.%d or newer", versionInfo.String(), path, doctorMinGoMajor, doctorMinGoMinor)
					check.SuggestedAction = "Install Go 1.26 or newer and ensure it appears first on PATH."
				}
			}
		}
	}
	return check
}

func doctorDockerChecks(ctx context.Context, deps doctorProbeDeps) []doctorCheck {
	path, err := deps.LookPath("docker")
	if err != nil {
		return []doctorCheck{{
			ID:              "docker.engine",
			Category:        "dependency",
			Name:            "Docker engine",
			Status:          doctorStatusWarn,
			Severity:        doctorSeverityOptional,
			Message:         "Docker CLI was not found; Docker engine cannot be probed",
			SuggestedAction: "Install Docker or configure non-Docker dev services when image-backed local services are needed.",
			Observed:        map[string]any{"command": "docker"},
		}}
	}
	return []doctorCheck{
		doctorDockerContextCheck(ctx, deps, path),
		doctorDockerEngineCheck(ctx, deps, path),
	}
}

func doctorDockerContextCheck(ctx context.Context, deps doctorProbeDeps, path string) doctorCheck {
	check := doctorCheck{
		ID:       "docker.context",
		Category: "dependency",
		Name:     "Docker context",
		Status:   doctorStatusOK,
		Severity: doctorSeverityInformational,
		Message:  "Docker context is selected",
		Observed: map[string]any{"command": "docker", "path": path},
	}
	cmdCtx, cancel := context.WithTimeout(ctx, doctorCommandTimeout)
	out, err := deps.RunCommand(cmdCtx, path, "context", "show")
	cancel()
	contextName := strings.TrimSpace(string(out))
	if contextName != "" {
		check.Observed["context"] = contextName
	}
	if err != nil {
		check.Status = doctorStatusWarn
		check.Severity = doctorSeverityOptional
		check.Message = "Docker CLI was found, but the current Docker context could not be determined"
		check.SuggestedAction = "Run `docker context show` manually and fix Docker context configuration if it fails."
		if contextName != "" {
			check.Observed["error_output"] = contextName
		}
		return check
	}
	if contextName != "" {
		check.Message = "Docker context " + contextName + " is selected"
	}
	return check
}

func doctorDockerEngineCheck(ctx context.Context, deps doctorProbeDeps, path string) doctorCheck {
	check := doctorCheck{
		ID:       "docker.engine",
		Category: "dependency",
		Name:     "Docker engine",
		Status:   doctorStatusOK,
		Severity: doctorSeverityOptional,
		Message:  "Docker engine is reachable",
		Observed: map[string]any{"command": "docker", "path": path},
	}
	cmdCtx, cancel := context.WithTimeout(ctx, doctorCommandTimeout)
	infoOut, infoErr := deps.RunCommand(cmdCtx, path, "info", "--format", "{{json .}}")
	cancel()
	if infoErr != nil {
		output := strings.TrimSpace(string(infoOut))
		check.Status = doctorStatusWarn
		check.Message = "Docker CLI was found, but the Docker engine is not reachable"
		check.SuggestedAction = "Start Docker Desktop or the Docker daemon, then rerun `scenery doctor --json`."
		if output != "" {
			check.Observed["error_output"] = output
		}
		return check
	}
	info := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(infoOut), &info); err != nil {
		output := strings.TrimSpace(string(infoOut))
		check.Status = doctorStatusWarn
		check.Message = "Docker engine responded, but engine details could not be parsed"
		check.SuggestedAction = "Run `docker info --format '{{json .}}'` manually and check the output."
		if output != "" {
			check.Observed["raw_output"] = output
		}
		return check
	}
	for source, target := range map[string]string{
		"ServerVersion":   "server_version",
		"OperatingSystem": "operating_system",
		"OSType":          "os_type",
		"Architecture":    "architecture",
		"NCPU":            "cpus",
		"MemTotal":        "memory_bytes",
		"DockerRootDir":   "docker_root_dir",
		"Driver":          "storage_driver",
		"CgroupVersion":   "cgroup_version",
		"KernelVersion":   "kernel_version",
		"Name":            "name",
	} {
		if value, ok := info[source]; ok && !doctorEmptyObservedValue(value) {
			check.Observed[target] = value
		}
	}
	if version, _ := check.Observed["server_version"].(string); version != "" {
		check.Message = "Docker Engine " + version + " is reachable"
	}
	return check
}

func doctorEmptyObservedValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}

type doctorGoVersion struct {
	Major int
	Minor int
	Patch int
}

var doctorGoVersionRE = regexp.MustCompile(`go([0-9]+)\.([0-9]+)(?:\.([0-9]+))?`)

func parseGoToolchainVersion(output string) (doctorGoVersion, bool) {
	match := doctorGoVersionRE.FindStringSubmatch(output)
	if len(match) == 0 {
		return doctorGoVersion{}, false
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return doctorGoVersion{}, false
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return doctorGoVersion{}, false
	}
	patch := 0
	if match[3] != "" {
		if patch, err = strconv.Atoi(match[3]); err != nil {
			return doctorGoVersion{}, false
		}
	}
	return doctorGoVersion{Major: major, Minor: minor, Patch: patch}, true
}

func (v doctorGoVersion) compare(other doctorGoVersion) int {
	switch {
	case v.Major != other.Major:
		return v.Major - other.Major
	case v.Minor != other.Minor:
		return v.Minor - other.Minor
	default:
		return v.Patch - other.Patch
	}
}

func (v doctorGoVersion) String() string {
	if v.Patch == 0 {
		return fmt.Sprintf("go%d.%d", v.Major, v.Minor)
	}
	return fmt.Sprintf("go%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func summarizeDoctorChecks(checks []doctorCheck) doctorSummary {
	var summary doctorSummary
	for _, check := range checks {
		switch check.Status {
		case doctorStatusOK:
			summary.OK++
		case doctorStatusWarn:
			summary.Warnings++
		case doctorStatusError:
			summary.Errors++
		case doctorStatusSkipped:
			summary.Skipped++
		}
	}
	return summary
}

func writeDoctorJSON(w io.Writer, resp doctorResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func writeDoctorText(w io.Writer, resp doctorResponse) error {
	if _, err := fmt.Fprintln(w, "scenery doctor"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, check := range resp.Checks {
		status := check.Status
		if status == doctorStatusWarn {
			status = "warn"
		}
		if _, err := fmt.Fprintf(w, "%-7s %-28s %s\n", status, check.ID, check.Message); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nsummary: %d ok, %d warnings, %d errors, %d skipped\n", resp.Summary.OK, resp.Summary.Warnings, resp.Summary.Errors, resp.Summary.Skipped)
	return err
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	for i, suffix := range units {
		value /= unit
		if value < unit || i == len(units)-1 {
			if value >= 10 {
				return fmt.Sprintf("%.0f %s", value, suffix)
			}
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", n)
}

type defaultDoctorResourceProbe struct{}

func (defaultDoctorResourceProbe) Runtime() doctorRuntimeInfo {
	return doctorRuntimeInfo{
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		NumCPU: runtime.NumCPU(),
	}
}
