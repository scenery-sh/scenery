package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
	"github.com/pbrazdil/onlava/internal/neonselfhost"
)

const (
	neonStatusSchemaVersion       = "onlava.db.neon.status.v1"
	neonCellSchemaVersion         = "onlava.db.neon.cell.v1"
	dbBranchRegistrySchemaVersion = "onlava.db.branch.registry.v1"
	dbBranchStatusSchemaVersion   = "onlava.db.branch.status.v1"
	dbBranchListSchemaVersion     = "onlava.db.branch.list.v1"
	dbBranchPinSchemaVersion      = "onlava.db.branch.v1"
	neonSelfhostProvider          = "neon-selfhost"
	neonDefaultMode               = "self-hosted"
	neonDefaultIsolation          = "branch"
	neonDefaultParentBranch       = "main"
	neonDefaultBranchPolicy       = "worktree"
	neonDefaultBranchNameTemplate = "{app}/{git_branch}"
	neonDefaultTTL                = "168h"
	neonDefaultDatabase           = "postgres"
	neonDefaultRole               = "cloud_admin"
	localPostgresBranchDriverEnv  = "ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER"
)

type dbNeonOptions struct {
	Command     string
	AppRoot     string
	JSON        bool
	DestroyData bool
}

type dbBranchOptions struct {
	Command string
	AppRoot string
	JSON    bool
	Branch  string
	Yes     bool
	Force   bool
	At      string
	After   string
	Older   string
	Target  string
}

type neonCellState struct {
	SchemaVersion string                `json:"schema_version"`
	Provider      string                `json:"provider"`
	Mode          string                `json:"mode"`
	Status        string                `json:"status"`
	Root          string                `json:"root"`
	ComposePath   string                `json:"compose_path"`
	LogDir        string                `json:"log_dir"`
	Storage       *neonStorageStatus    `json:"storage,omitempty"`
	Driver        *neonCellDriver       `json:"driver,omitempty"`
	Ports         map[string]int        `json:"ports,omitempty"`
	Images        []neonImageStatus     `json:"images"`
	Components    []neonComponentStatus `json:"components"`
	CreatedAt     string                `json:"created_at,omitempty"`
	UpdatedAt     string                `json:"updated_at,omitempty"`
}

type neonCellDriver struct {
	Kind    string `json:"kind"`
	Tool    string `json:"tool"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type dbNeonStatusResult struct {
	SchemaVersion  string                `json:"schema_version"`
	OK             bool                  `json:"ok"`
	Provider       string                `json:"provider"`
	Mode           string                `json:"mode"`
	Status         string                `json:"status"`
	Root           string                `json:"root"`
	Storage        *neonStorageStatus    `json:"storage,omitempty"`
	Driver         *neonCellDriver       `json:"driver,omitempty"`
	Backend        *neonBackendStatus    `json:"backend,omitempty"`
	Cell           *neonCellState        `json:"cell,omitempty"`
	GeneratedFiles []neonGeneratedFile   `json:"generated_files,omitempty"`
	Images         []neonImageStatus     `json:"images"`
	Components     []neonComponentStatus `json:"components"`
	Checks         []neonHealthCheck     `json:"checks,omitempty"`
	Ports          map[string]int        `json:"ports,omitempty"`
	LogDir         string                `json:"log_dir,omitempty"`
	Message        string                `json:"message,omitempty"`
	RequiredAction string                `json:"required_action,omitempty"`
}

type neonGeneratedFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type neonStorageStatus struct {
	Mode     string            `json:"mode"`
	Root     string            `json:"root"`
	DataDirs map[string]string `json:"data_dirs"`
}

type neonImageStatus struct {
	Name      string `json:"name"`
	Ref       string `json:"ref"`
	Optional  bool   `json:"optional"`
	Stability string `json:"stability"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
}

type neonComponentStatus struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Log       string `json:"log,omitempty"`
	Container string `json:"container,omitempty"`
	Health    string `json:"health,omitempty"`
	Message   string `json:"message,omitempty"`
}

type neonHealthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type neonBackendStatus struct {
	SchemaVersion string         `json:"schema_version"`
	Present       bool           `json:"present"`
	TenantID      string         `json:"tenant_id,omitempty"`
	BranchCount   int            `json:"branch_count"`
	ComputeCount  int            `json:"compute_count"`
	Statuses      map[string]int `json:"statuses,omitempty"`
	Message       string         `json:"message,omitempty"`
}

type worktreeDBPin struct {
	SchemaVersion string `json:"schema_version"`
	Provider      string `json:"provider"`
	Project       string `json:"project"`
	ParentBranch  string `json:"parent_branch"`
	Branch        string `json:"branch"`
	BranchID      string `json:"branch_id"`
	Database      string `json:"database"`
	Role          string `json:"role"`
	SessionID     string `json:"session_id,omitempty"`
	WorktreeRoot  string `json:"worktree_root,omitempty"`
	CreatedBy     string `json:"created_by"`
	TTL           string `json:"ttl,omitempty"`
}

type neonBranchRegistry struct {
	SchemaVersion string            `json:"schema_version"`
	Provider      string            `json:"provider"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
	Leases        []neonBranchLease `json:"leases"`
}

type neonBranchLease struct {
	Pin       worktreeDBPin `json:"pin"`
	Status    string        `json:"status"`
	Endpoint  *neonEndpoint `json:"endpoint,omitempty"`
	CreatedAt string        `json:"created_at,omitempty"`
	UpdatedAt string        `json:"updated_at,omitempty"`
	ExpiresAt string        `json:"expires_at,omitempty"`
}

type neonEndpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Role     string `json:"role"`
	SSLMode  string `json:"sslmode,omitempty"`
	Source   string `json:"source,omitempty"`
}

type dbBranchStatusResult struct {
	SchemaVersion  string             `json:"schema_version"`
	OK             bool               `json:"ok"`
	App            inspectdata.AppRef `json:"app"`
	Provider       string             `json:"provider"`
	Status         string             `json:"status"`
	BackendStatus  string             `json:"backend_status,omitempty"`
	BackendMessage string             `json:"backend_message,omitempty"`
	Connection     *neonEndpoint      `json:"connection,omitempty"`
	PinPath        string             `json:"pin_path"`
	Pin            *worktreeDBPin     `json:"pin,omitempty"`
	DatabaseURLEnv string             `json:"database_url_env"`
	PSQLCommand    string             `json:"psql_command"`
	ResetCommand   string             `json:"reset_command"`
	Message        string             `json:"message,omitempty"`
}

type dbBranchListResult struct {
	SchemaVersion string              `json:"schema_version"`
	OK            bool                `json:"ok"`
	App           inspectdata.AppRef  `json:"app"`
	Provider      string              `json:"provider"`
	Branches      []worktreeDBPin     `json:"branches"`
	Leases        []dbBranchListLease `json:"leases,omitempty"`
	RegistryPath  string              `json:"registry_path,omitempty"`
	Message       string              `json:"message,omitempty"`
}

type dbBranchListLease struct {
	Pin       worktreeDBPin `json:"pin"`
	Status    string        `json:"status"`
	Endpoint  *neonEndpoint `json:"endpoint,omitempty"`
	CreatedAt string        `json:"created_at,omitempty"`
	UpdatedAt string        `json:"updated_at,omitempty"`
	ExpiresAt string        `json:"expires_at,omitempty"`
}

type neonBranchResolution struct {
	Pin           worktreeDBPin
	Source        string
	Created       bool
	BackendStatus neonBranchBackendStatus
}

type neonBranchBackendStatus struct {
	Status   string
	Message  string
	Endpoint *neonEndpoint
}

type neonBranchConnectionInfo struct {
	DatabaseURL  string
	DatabaseName string
	Endpoint     neonEndpoint
}

type neonBranchProvider interface {
	EnsureBranch(context.Context, worktreeDBPin) (neonBranchBackendStatus, error)
	InspectBranch(context.Context, worktreeDBPin) neonBranchBackendStatus
	Connection(context.Context, worktreeDBPin) (neonBranchConnectionInfo, error)
	ResetBranch(context.Context, worktreeDBPin, dbBranchOptions) error
	DeleteBranch(context.Context, worktreeDBPin, string, dbBranchOptions) error
	RestoreBranch(context.Context, worktreeDBPin, dbBranchOptions) (neonBranchRestorePoint, error)
	DiffBranch(context.Context, worktreeDBPin, string, dbBranchOptions) (string, error)
}

type neonSelfhostBranchProvider struct{}

var neonDockerCommand = "docker"

func dbNeonCommand(args []string) error {
	return runDBNeonCommand(context.Background(), os.Stdout, args)
}

func runDBNeonCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseDBNeonArgs(args)
	if err != nil {
		return err
	}
	switch opts.Command {
	case "install":
		return runDBNeonInstall(ctx, stdout, opts)
	case "start":
		return runDBNeonStart(ctx, stdout, opts)
	case "status":
		return runDBNeonStatus(ctx, stdout, opts)
	case "logs":
		return runDBNeonLogs(ctx, stdout, opts)
	case "stop":
		return runDBNeonStop(ctx, stdout, opts)
	case "restart":
		return runDBNeonRestart(ctx, stdout, opts)
	case "uninstall":
		return runDBNeonUninstall(ctx, stdout, opts)
	default:
		return fmt.Errorf("unknown db neon command %q", opts.Command)
	}
}

func parseDBNeonArgs(args []string) (dbNeonOptions, error) {
	if len(args) == 0 {
		return dbNeonOptions{}, fmt.Errorf("usage: onlava db neon install|start|status|logs|stop|restart|uninstall [--json]")
	}
	opts := dbNeonOptions{Command: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbNeonOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--destroy-data":
			opts.DestroyData = true
		default:
			return dbNeonOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	switch opts.Command {
	case "install", "start", "status", "logs", "stop", "restart", "uninstall":
	default:
		return dbNeonOptions{}, fmt.Errorf("unknown db neon command %q", opts.Command)
	}
	return opts, nil
}

func runDBNeonInstall(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		return err
	}
	state := defaultNeonCellState(root, "installed")
	if err := ensureNeonStorageDirs(root); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	state.CreatedAt = now
	state.UpdatedAt = now
	driver := ensureBuiltinNeonSelfhostDriver(ctx)
	state.Driver = &driver
	if err := writeGeneratedNeonFiles(state); err != nil {
		return err
	}
	if err := writeNeonCellState(state); err != nil {
		return err
	}
	if err := ensureNeonBranchRegistry(root); err != nil {
		return err
	}
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	result.Message = "Neon dev-cell files installed. Runtime startup is available through `onlava db neon start`, and readiness still depends on status health checks."
	result.RequiredAction = "Run `onlava db neon start --json` to start the generated dev-cell project, then inspect readiness with `onlava db neon status --json`."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "installed Neon dev-cell files at %s\n", root)
	return nil
}

func runDBNeonStart(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	state, ok, err := readNeonCellState(root)
	if err != nil {
		return err
	}
	if !ok {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "Neon dev-cell state is not installed; nothing can be started."
		result.RequiredAction = "Run `onlava db neon install --json` first."
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	if fileStatus(state.ComposePath) != "present" {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "Neon dev-cell compose file is missing; cannot start generated project."
		result.RequiredAction = "Run `onlava db neon install --json` again to regenerate local substrate files."
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	if legacy, err := legacyAnonymousNeonDataVolumes(ctx); err != nil {
		return err
	} else if len(legacy) > 0 {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Status = "degraded"
		result.Message = "Existing Onlava Neon containers still use Docker-managed /data volumes; this storage layout requires a fresh bind-mounted cell."
		result.RequiredAction = "Run `onlava db neon uninstall --destroy-data --json`, then `onlava db neon install --json` and `onlava db neon start --json` to start fresh with bind-mounted storage."
		result.Checks = append(result.Checks, neonHealthCheck{Name: "storage.legacy_volumes", Status: "blocked", Message: strings.Join(legacy, ", ")})
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	if _, err := runDockerCompose(ctx, 90*time.Second, state, "up", "-d"); err != nil {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "Failed to start generated Neon dev-cell project: " + err.Error()
		result.RequiredAction = "Inspect Docker Compose output and component logs with `onlava db neon logs`."
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeNeonCellState(state); err != nil {
		return err
	}
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	result.Message = "Started generated Neon dev-cell project. Readiness is based on Docker and listener health checks."
	result.RequiredAction = "Use `onlava db neon status --json` to inspect component readiness before relying on branch leases."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintln(stdout, "started Neon dev-cell project")
	return nil
}

func runDBNeonStatus(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "neon %s at %s\n", result.Status, result.Root)
	if result.Message != "" {
		fmt.Fprintln(stdout, result.Message)
	}
	return nil
}

func runDBNeonLogs(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	if result.LogDir == "" {
		return fmt.Errorf("Neon dev-cell logs are not available; run `onlava db neon install --json` first")
	}
	fmt.Fprintln(stdout, result.LogDir)
	return nil
}

func runDBNeonStop(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	state, ok, err := readNeonCellState(root)
	if err != nil {
		return err
	}
	if !ok {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "Neon dev-cell state is not installed; nothing can be stopped."
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	if _, err := runDockerCompose(ctx, 45*time.Second, state, "stop"); err != nil {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "Failed to stop generated Neon dev-cell project: " + err.Error()
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeNeonCellState(state); err != nil {
		return err
	}
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	result.Message = "Stopped generated Neon dev-cell project. Local substrate files and branch leases were left in place."
	result.RequiredAction = "Run `onlava db neon start --json` to start the shared dev cell again."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintln(stdout, "stopped Neon dev-cell project")
	return nil
}

func runDBNeonRestart(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	state, ok, err := readNeonCellState(root)
	if err != nil {
		return err
	}
	if !ok {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "Neon dev-cell state is not installed; nothing can be restarted."
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	statuses, err := dockerContainerStatuses(ctx)
	if err != nil {
		return err
	}
	components := cloneNeonComponents(state.Components)
	if len(components) == 0 {
		components = defaultNeonComponents(root, "not_started")
	}
	containers := make([]string, 0, len(components))
	for _, component := range components {
		if component.Container == "" {
			continue
		}
		if _, ok := statuses[component.Container]; ok {
			containers = append(containers, component.Container)
		}
	}
	if len(containers) == 0 {
		result, statusErr := buildDBNeonStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		result.OK = false
		result.Message = "No Onlava-owned Neon dev-cell containers exist to restart."
		result.RequiredAction = "Run `onlava db neon status --json` to inspect generated state and container health."
		if opts.JSON {
			if err := writeInspectJSON(stdout, result); err != nil {
				return err
			}
			return &silentCLIError{err: errors.New(result.Message)}
		}
		return errors.New(result.Message)
	}
	for _, container := range containers {
		if _, err := runDockerCommand(ctx, 15*time.Second, "restart", container); err != nil {
			return err
		}
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeNeonCellState(state); err != nil {
		return err
	}
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	result.Message = fmt.Sprintf("Restarted %d Onlava-owned Neon dev-cell container(s).", len(containers))
	result.RequiredAction = "Run `onlava db neon status --json` to inspect post-restart health."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "restarted %d Neon dev-cell container(s)\n", len(containers))
	return nil
}

func runDBNeonUninstall(ctx context.Context, stdout io.Writer, opts dbNeonOptions) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	state, ok, stateErr := readNeonCellState(root)
	teardownAttempted := false
	if stateErr != nil {
		state = defaultNeonCellState(root, "installed")
		ok = true
	}
	if ok && fileStatus(state.ComposePath) == "present" {
		teardownAttempted = true
		args := []string{"down", "--remove-orphans"}
		if opts.DestroyData {
			args = []string{"down", "-v", "--remove-orphans"}
		}
		if _, err := runDockerCompose(ctx, 90*time.Second, state, args...); err != nil {
			return fmt.Errorf("stop Neon dev-cell before uninstall: %w", err)
		}
	}
	if !teardownAttempted {
		if err := removeOnlavaNeonContainers(ctx, opts.DestroyData); err != nil {
			return fmt.Errorf("remove Neon dev-cell containers before uninstall: %w", err)
		}
	} else if err := removeOnlavaNeonContainers(ctx, opts.DestroyData); err != nil {
		return fmt.Errorf("remove remaining Neon dev-cell containers before uninstall: %w", err)
	}
	if opts.DestroyData {
		if err := os.RemoveAll(root); err != nil {
			return err
		}
	} else if err := removeNeonGeneratedStatePreservingData(root); err != nil {
		return err
	}
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	if opts.DestroyData {
		result.Message = "Neon dev-cell state and bind-mounted storage data removed."
	} else {
		result.Message = "Neon dev-cell state removed. Bind-mounted storage data was preserved."
		result.RequiredAction = "Run `onlava db neon install --json` to regenerate runtime files around the preserved data, or rerun with --destroy-data to remove data."
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "removed Neon dev-cell state at %s\n", root)
	return nil
}

func buildDBNeonStatus(ctx context.Context) (dbNeonStatusResult, error) {
	root, err := neonSubstrateRoot()
	if err != nil {
		return dbNeonStatusResult{}, err
	}
	state, ok, err := readNeonCellState(root)
	if err != nil {
		return dbNeonStatusResult{}, err
	}
	result := dbNeonStatusResult{
		SchemaVersion:  neonStatusSchemaVersion,
		OK:             ok,
		Provider:       neonSelfhostProvider,
		Mode:           neonDefaultMode,
		Status:         "not_installed",
		Root:           root,
		Images:         defaultNeonImages(),
		Components:     defaultNeonComponents(root, "not_started"),
		Message:        "Neon dev-cell state is not installed.",
		RequiredAction: "Run `onlava db neon install --json` to create generated local state files.",
	}
	if !ok {
		return result, nil
	}
	generatedFiles := []neonGeneratedFile{
		{Path: filepath.Join(root, "cell.json"), Kind: "state", Status: fileStatus(filepath.Join(root, "cell.json"))},
		{Path: state.ComposePath, Kind: "compose", Status: fileStatus(state.ComposePath)},
		{Path: neonStorageRoot(root), Kind: "storage-root", Status: fileStatus(neonStorageRoot(root))},
		{Path: filepath.Join(root, "pageserver_config", "pageserver.toml"), Kind: "config", Status: fileStatus(filepath.Join(root, "pageserver_config", "pageserver.toml"))},
		{Path: filepath.Join(root, "pageserver_config", "identity.toml"), Kind: "config", Status: fileStatus(filepath.Join(root, "pageserver_config", "identity.toml"))},
		{Path: filepath.Join(root, "compute_templates", "config.json"), Kind: "template", Status: fileStatus(filepath.Join(root, "compute_templates", "config.json"))},
		{Path: filepath.Join(root, "compute_templates", "compute.sh"), Kind: "template", Status: fileStatus(filepath.Join(root, "compute_templates", "compute.sh"))},
		{Path: filepath.Join(root, "backend.json"), Kind: "backend-state", Status: fileStatus(filepath.Join(root, "backend.json"))},
		{Path: neonBranchRegistryPath(root), Kind: "branch-registry", Status: fileStatus(neonBranchRegistryPath(root))},
	}
	storageDirs := neonStorageDirs(root)
	for _, name := range neonStorageDirNames() {
		path := storageDirs[name]
		generatedFiles = append(generatedFiles, neonGeneratedFile{Path: path, Kind: "storage-dir", Status: fileStatus(path)})
	}
	images, components, checks := probeNeonRuntime(ctx, state)
	backend := buildNeonBackendStatus(root)
	status := firstNonEmpty(state.Status, "installed")
	if generatedFilesMissing(generatedFiles) {
		status = "degraded"
	} else if backend != nil && backend.Present && backend.Message != "" {
		status = "degraded"
	} else if componentStatusesInclude(components, "exited") {
		status = "exited"
	} else if componentStatusesInclude(components, "degraded") {
		status = "degraded"
	} else if componentsAllRunning(components) {
		status = "ready"
	} else if componentsPartiallyRunning(components) {
		status = "degraded"
	}
	cell := state
	cell.Status = status
	if len(cell.Ports) == 0 {
		cell.Ports = defaultNeonPorts()
	}
	if cell.Storage == nil {
		storage := neonStorageStatusForRoot(root)
		cell.Storage = &storage
	}
	if cell.Driver == nil {
		driver := inspectBuiltinNeonSelfhostDriver()
		cell.Driver = &driver
	}
	cell.Images = images
	cell.Components = components
	result.OK = true
	result.Status = status
	result.Storage = cell.Storage
	result.Driver = cell.Driver
	result.Backend = backend
	result.Cell = &cell
	result.Images = images
	result.Components = components
	result.Checks = checks
	result.Ports = cloneNeonPorts(cell.Ports)
	result.LogDir = state.LogDir
	result.GeneratedFiles = generatedFiles
	switch status {
	case "ready":
		result.Message = "Neon dev-cell storage containers are running. Branch compute readiness is managed by the configured branch driver during checkout or app startup."
		result.RequiredAction = "Run `onlava db branch checkout <name> --json` or `onlava up` to ensure a branch compute endpoint, then inspect with `onlava db branch status --json`."
	case "degraded", "exited":
		result.OK = false
		result.Message = "Neon dev-cell generated state is present, but runtime health is degraded."
		result.RequiredAction = "Inspect generated files, Docker availability, image presence, and component log paths before starting branch-provider work."
	default:
		result.Message = "Neon dev-cell files are installed; storage runtime has not been started yet."
		result.RequiredAction = "Run `onlava db neon start --json`; branch compute startup requires the storage cell to become reachable."
	}
	return result, nil
}

func neonSubstrateRoot() (string, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.AgentDir, "substrates", "neon"), nil
}

func removeNeonGeneratedStatePreservingData(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "data" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func defaultNeonCellState(root, status string) neonCellState {
	storage := neonStorageStatusForRoot(root)
	return neonCellState{
		SchemaVersion: neonCellSchemaVersion,
		Provider:      neonSelfhostProvider,
		Mode:          neonDefaultMode,
		Status:        status,
		Root:          root,
		ComposePath:   filepath.Join(root, "compose.generated.yml"),
		LogDir:        filepath.Join(root, "logs"),
		Storage:       &storage,
		Ports:         defaultNeonPorts(),
		Images:        defaultNeonImages(),
		Components:    defaultNeonComponents(root, "not_started"),
	}
}

func neonStorageRoot(root string) string {
	return filepath.Join(root, "data")
}

func neonStorageDirs(root string) map[string]string {
	base := neonStorageRoot(root)
	dirs := make(map[string]string, len(neonStorageDirNames()))
	for _, name := range neonStorageDirNames() {
		dirs[name] = filepath.Join(base, name)
	}
	return dirs
}

func neonStorageDirNames() []string {
	return []string{"minio", "pageserver", "safekeeper-1", "safekeeper-2", "safekeeper-3", "storage-broker"}
}

func neonStorageStatusForRoot(root string) neonStorageStatus {
	return neonStorageStatus{
		Mode:     "bind",
		Root:     neonStorageRoot(root),
		DataDirs: neonStorageDirs(root),
	}
}

func ensureNeonStorageDirs(root string) error {
	dirs := neonStorageDirs(root)
	for _, name := range neonStorageDirNames() {
		path := dirs[name]
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func defaultNeonPorts() map[string]int {
	return map[string]int{
		"minio_api":       55430,
		"minio_console":   55431,
		"storage_broker":  55432,
		"pageserver_http": 55434,
		"safekeeper_1":    55435,
		"safekeeper_2":    55436,
		"safekeeper_3":    55437,
	}
}

func defaultNeonImages() []neonImageStatus {
	return []neonImageStatus{
		{Name: "neon", Ref: "ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f", Optional: true, Stability: "unstable"},
		{Name: "neon-compute-node-v16", Ref: "ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249", Optional: true, Stability: "unstable"},
		{Name: "minio", Ref: "quay.io/minio/minio:RELEASE.2022-10-20T00-55-09Z", Optional: true, Stability: "unstable"},
		{Name: "minio-client", Ref: "minio/mc@sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727", Optional: true, Stability: "unstable"},
	}
}

func defaultNeonComponents(root, status string) []neonComponentStatus {
	log := func(name string) string { return filepath.Join(root, "logs", name+".log") }
	return []neonComponentStatus{
		{Name: "minio", Role: "object-storage", Status: status, Log: log("minio"), Container: "onlava-neon-minio"},
		{Name: "bucket-init", Role: "init", Status: status, Log: log("bucket-init"), Container: "onlava-neon-bucket-init"},
		{Name: "pageserver", Role: "storage", Status: status, Log: log("pageserver"), Container: "onlava-neon-pageserver"},
		{Name: "safekeeper-1", Role: "wal", Status: status, Log: log("safekeeper-1"), Container: "onlava-neon-safekeeper-1"},
		{Name: "safekeeper-2", Role: "wal", Status: status, Log: log("safekeeper-2"), Container: "onlava-neon-safekeeper-2"},
		{Name: "safekeeper-3", Role: "wal", Status: status, Log: log("safekeeper-3"), Container: "onlava-neon-safekeeper-3"},
		{Name: "storage-broker", Role: "control-plane", Status: status, Log: log("storage-broker"), Container: "onlava-neon-storage-broker"},
	}
}

func readNeonCellState(root string) (neonCellState, bool, error) {
	path := filepath.Join(root, "cell.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return neonCellState{}, false, nil
	}
	if err != nil {
		return neonCellState{}, false, err
	}
	var state neonCellState
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return neonCellState{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if state.SchemaVersion != neonCellSchemaVersion {
		return neonCellState{}, false, fmt.Errorf("%s has unsupported schema_version %q", path, state.SchemaVersion)
	}
	return state, true, nil
}

func writeNeonCellState(state neonCellState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(filepath.Join(state.Root, "cell.json"), data, 0o644)
}

func buildNeonBackendStatus(root string) *neonBackendStatus {
	summary := &neonBackendStatus{
		SchemaVersion: neonselfhost.BackendSchemaVersion,
		Present:       false,
		Statuses:      map[string]int{},
	}
	state, ok, err := neonselfhost.ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		summary.Present = true
		summary.Message = err.Error()
		return summary
	}
	if !ok {
		summary.Message = "backend.json is not installed yet"
		return summary
	}
	summary.Present = true
	summary.TenantID = state.TenantID
	summary.BranchCount = len(state.Branches)
	for _, branch := range state.Branches {
		if strings.TrimSpace(branch.ComputeContainer) != "" {
			summary.ComputeCount++
		}
		status := firstNonEmpty(branch.Status, "unknown")
		summary.Statuses[status]++
	}
	if len(summary.Statuses) == 0 {
		summary.Statuses = nil
	}
	return summary
}

func fileStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "present"
	}
	return "missing"
}

func neonPostgresService(cfg appcfg.Config) appcfg.DevServiceConfig {
	if _, svc, ok := managedPostgresDeclared(cfg); ok {
		return svc
	}
	return appcfg.DevServiceConfig{}
}

func inspectAppRef(appRoot string, cfg appcfg.Config) inspectdata.AppRef {
	return inspectdata.AppRef{
		Name:       cfg.Name,
		ID:         cfg.ID,
		Root:       appRoot,
		ConfigPath: filepath.Join(appRoot, ".onlava.json"),
	}
}
