package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
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
	Ports         map[string]int        `json:"ports,omitempty"`
	Images        []neonImageStatus     `json:"images"`
	Components    []neonComponentStatus `json:"components"`
	CreatedAt     string                `json:"created_at,omitempty"`
	UpdatedAt     string                `json:"updated_at,omitempty"`
}

type dbNeonStatusResult struct {
	SchemaVersion  string                `json:"schema_version"`
	OK             bool                  `json:"ok"`
	Provider       string                `json:"provider"`
	Mode           string                `json:"mode"`
	Status         string                `json:"status"`
	Root           string                `json:"root"`
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
	_ = ctx
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		return err
	}
	state := defaultNeonCellState(root, "installed")
	now := time.Now().UTC().Format(time.RFC3339)
	state.CreatedAt = now
	state.UpdatedAt = now
	if err := writeGeneratedNeonCompose(state.ComposePath); err != nil {
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
	if !opts.DestroyData {
		return fmt.Errorf("onlava db neon uninstall requires --destroy-data")
	}
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
		if _, err := runDockerCompose(ctx, 90*time.Second, state, "down", "-v", "--remove-orphans"); err != nil {
			return fmt.Errorf("stop Neon dev-cell before uninstall: %w", err)
		}
	}
	if !teardownAttempted {
		if err := removeOnlavaNeonContainers(ctx); err != nil {
			return fmt.Errorf("remove Neon dev-cell containers before uninstall: %w", err)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	result, err := buildDBNeonStatus(ctx)
	if err != nil {
		return err
	}
	result.Message = "Neon dev-cell state removed."
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
		{Path: neonBranchRegistryPath(root), Kind: "branch-registry", Status: fileStatus(neonBranchRegistryPath(root))},
	}
	images, components, checks := probeNeonRuntime(ctx, state)
	status := firstNonEmpty(state.Status, "installed")
	if generatedFilesMissing(generatedFiles) {
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
	cell.Images = images
	cell.Components = components
	result.OK = true
	result.Status = status
	result.Cell = &cell
	result.Images = images
	result.Components = components
	result.Checks = checks
	result.Ports = cloneNeonPorts(cell.Ports)
	result.LogDir = state.LogDir
	result.GeneratedFiles = generatedFiles
	switch status {
	case "ready":
		result.Message = "Neon dev-cell containers are running, but branch-backed app startup is still pending branch-provider integration."
		result.RequiredAction = "Continue with the branch-provider milestone before relying on app sessions or db psql against Neon."
	case "degraded", "exited":
		result.OK = false
		result.Message = "Neon dev-cell generated state is present, but runtime health is degraded."
		result.RequiredAction = "Inspect generated files, Docker availability, image presence, and component log paths before starting branch-provider work."
	default:
		result.Message = "Neon dev-cell files are installed; runtime startup is pending implementation."
		result.RequiredAction = "Status now checks Docker, optional images, generated files, and labeled containers; branch-provider integration is still pending."
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

func defaultNeonCellState(root, status string) neonCellState {
	return neonCellState{
		SchemaVersion: neonCellSchemaVersion,
		Provider:      neonSelfhostProvider,
		Mode:          neonDefaultMode,
		Status:        status,
		Root:          root,
		ComposePath:   filepath.Join(root, "compose.generated.yml"),
		LogDir:        filepath.Join(root, "logs"),
		Ports:         defaultNeonPorts(),
		Images:        defaultNeonImages(),
		Components:    defaultNeonComponents(root, "not_started"),
	}
}

func defaultNeonPorts() map[string]int {
	return map[string]int{
		"minio_api":        55430,
		"minio_console":    55431,
		"storage_broker":   55432,
		"compute_postgres": 55433,
		"pageserver_http":  55434,
		"safekeeper_1":     55435,
		"safekeeper_2":     55436,
		"safekeeper_3":     55437,
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
		{Name: "pageserver", Role: "storage", Status: status, Log: log("pageserver"), Container: "onlava-neon-pageserver"},
		{Name: "safekeeper-1", Role: "wal", Status: status, Log: log("safekeeper-1"), Container: "onlava-neon-safekeeper-1"},
		{Name: "safekeeper-2", Role: "wal", Status: status, Log: log("safekeeper-2"), Container: "onlava-neon-safekeeper-2"},
		{Name: "safekeeper-3", Role: "wal", Status: status, Log: log("safekeeper-3"), Container: "onlava-neon-safekeeper-3"},
		{Name: "storage-broker", Role: "control-plane", Status: status, Log: log("storage-broker"), Container: "onlava-neon-storage-broker"},
		{Name: "compute", Role: "database", Status: status, Log: log("compute"), Container: "onlava-neon-compute"},
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

func writeGeneratedNeonCompose(path string) error {
	text := `# Generated by onlava. Do not edit for normal operation.
# This dev-cell file records the component topology used by onlava db neon start.
# Readiness is determined by onlava db neon status --json.
services:
  minio:
    image: quay.io/minio/minio:RELEASE.2022-10-20T00-55-09Z
    container_name: onlava-neon-minio
    ports:
      - "127.0.0.1:55430:9000"
      - "127.0.0.1:55431:9001"
    labels:
      onlava.substrate: neon
      onlava.component: minio
  pageserver:
    image: ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f
    container_name: onlava-neon-pageserver
    ports:
      - "127.0.0.1:55434:9898"
    labels:
      onlava.substrate: neon
      onlava.component: pageserver
  safekeeper1:
    image: ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f
    container_name: onlava-neon-safekeeper-1
    ports:
      - "127.0.0.1:55435:5454"
    labels:
      onlava.substrate: neon
      onlava.component: safekeeper-1
  safekeeper2:
    image: ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f
    container_name: onlava-neon-safekeeper-2
    ports:
      - "127.0.0.1:55436:5454"
    labels:
      onlava.substrate: neon
      onlava.component: safekeeper-2
  safekeeper3:
    image: ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f
    container_name: onlava-neon-safekeeper-3
    ports:
      - "127.0.0.1:55437:5454"
    labels:
      onlava.substrate: neon
      onlava.component: safekeeper-3
  storage_broker:
    image: ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f
    container_name: onlava-neon-storage-broker
    ports:
      - "127.0.0.1:55432:50051"
    labels:
      onlava.substrate: neon
      onlava.component: storage-broker
  compute:
    image: ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249
    container_name: onlava-neon-compute
    ports:
      - "127.0.0.1:55433:5432"
    labels:
      onlava.substrate: neon
      onlava.component: compute
`
	return atomicWriteFile(path, []byte(text), 0o644)
}

func probeNeonRuntime(ctx context.Context, state neonCellState) ([]neonImageStatus, []neonComponentStatus, []neonHealthCheck) {
	images := cloneNeonImages(state.Images)
	if len(images) == 0 {
		images = defaultNeonImages()
	}
	components := cloneNeonComponents(state.Components)
	if len(components) == 0 {
		components = defaultNeonComponents(state.Root, "not_started")
	}
	checks := []neonHealthCheck{}
	if _, err := exec.LookPath(neonDockerCommand); err != nil {
		checks = append(checks, neonHealthCheck{Name: "docker", Status: "missing", Message: "docker CLI not found on PATH"})
		markImagesUnknown(images, "docker CLI not found")
		markComponentsNotStarted(components, "docker CLI not found")
		return images, components, checks
	}
	if output, err := runDockerProbe(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		checks = append(checks, neonHealthCheck{Name: "docker", Status: "unavailable", Message: err.Error()})
		markImagesUnknown(images, "docker daemon unavailable")
		markComponentsNotStarted(components, "docker daemon unavailable")
		return images, components, checks
	} else {
		checks = append(checks, neonHealthCheck{Name: "docker", Status: "available", Message: strings.TrimSpace(output)})
	}
	for i := range images {
		if _, err := runDockerProbe(ctx, "image", "inspect", images[i].Ref); err != nil {
			images[i].Status = "missing"
			images[i].Message = err.Error()
			continue
		}
		images[i].Status = "present"
	}
	containerStatus, err := dockerContainerStatuses(ctx)
	if err != nil {
		checks = append(checks, neonHealthCheck{Name: "containers", Status: "unavailable", Message: err.Error()})
		markComponentsNotStarted(components, "could not inspect Docker containers")
		return images, components, checks
	}
	checks = append(checks, neonHealthCheck{Name: "containers", Status: "inspected"})
	for i := range components {
		status, ok := containerStatus[components[i].Container]
		if !ok {
			components[i].Status = "not_started"
			continue
		}
		components[i].Message = status
		components[i].Health = dockerHealthFromStatus(status)
		switch {
		case strings.HasPrefix(status, "Up "):
			components[i].Status = "running"
			if components[i].Health == "unhealthy" {
				components[i].Status = "degraded"
			}
		case strings.HasPrefix(status, "Exited ") || strings.HasPrefix(status, "Dead"):
			components[i].Status = "exited"
		default:
			components[i].Status = "unknown"
		}
	}
	portChecks := probeNeonPorts(ctx, firstPorts(state.Ports), components)
	checks = append(checks, portChecks...)
	for _, check := range portChecks {
		if check.Status != "closed" {
			continue
		}
		componentName := strings.TrimPrefix(check.Name, "port.")
		for i := range components {
			if components[i].Name != componentName {
				continue
			}
			components[i].Status = "degraded"
			if components[i].Message == "" {
				components[i].Message = check.Message
			} else if check.Message != "" {
				components[i].Message += "; " + check.Message
			}
			break
		}
	}
	return images, components, checks
}

func firstPorts(ports map[string]int) map[string]int {
	if len(ports) == 0 {
		return defaultNeonPorts()
	}
	return ports
}

func probeNeonPorts(ctx context.Context, ports map[string]int, components []neonComponentStatus) []neonHealthCheck {
	portKeys := map[string]string{
		"minio":          "minio_api",
		"pageserver":     "pageserver_http",
		"safekeeper-1":   "safekeeper_1",
		"safekeeper-2":   "safekeeper_2",
		"safekeeper-3":   "safekeeper_3",
		"storage-broker": "storage_broker",
		"compute":        "compute_postgres",
	}
	checks := make([]neonHealthCheck, 0, len(portKeys))
	for _, component := range components {
		key, ok := portKeys[component.Name]
		if !ok {
			continue
		}
		port := ports[key]
		if port == 0 || component.Status != "running" {
			continue
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if err := probeTCPPort(ctx, addr); err != nil {
			checks = append(checks, neonHealthCheck{Name: "port." + component.Name, Status: "closed", Message: addr + ": " + err.Error()})
			continue
		}
		checks = append(checks, neonHealthCheck{Name: "port." + component.Name, Status: "open", Message: addr})
	}
	return checks
}

func probeTCPPort(ctx context.Context, addr string) error {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

func cloneNeonImages(images []neonImageStatus) []neonImageStatus {
	out := make([]neonImageStatus, len(images))
	copy(out, images)
	return out
}

func cloneNeonComponents(components []neonComponentStatus) []neonComponentStatus {
	out := make([]neonComponentStatus, len(components))
	copy(out, components)
	return out
}

func cloneNeonPorts(ports map[string]int) map[string]int {
	if len(ports) == 0 {
		return nil
	}
	out := make(map[string]int, len(ports))
	for key, value := range ports {
		out[key] = value
	}
	return out
}

func cloneNeonEndpoint(endpoint *neonEndpoint) *neonEndpoint {
	if endpoint == nil {
		return nil
	}
	out := *endpoint
	return &out
}

func runDockerProbe(ctx context.Context, args ...string) (string, error) {
	return runDockerCommand(ctx, 3*time.Second, args...)
}

func runDockerCommand(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, neonDockerCommand, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runDockerCompose(ctx context.Context, timeout time.Duration, state neonCellState, args ...string) (string, error) {
	composePath := strings.TrimSpace(state.ComposePath)
	if composePath == "" {
		return "", errors.New("missing Neon compose path in cell state")
	}
	dockerArgs := []string{"compose", "-f", composePath, "-p", "onlava-neon"}
	dockerArgs = append(dockerArgs, args...)
	return runDockerCommand(ctx, timeout, dockerArgs...)
}

func dockerContainerStatuses(ctx context.Context) (map[string]string, error) {
	output, err := runDockerProbe(ctx, "ps", "-a", "--filter", "label=onlava.substrate=neon", "--format", "{{.Names}}\t{{.Status}}")
	if err != nil {
		return nil, err
	}
	statuses := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, status, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		statuses[strings.TrimSpace(name)] = strings.TrimSpace(status)
	}
	return statuses, nil
}

func onlavaNeonContainerNames(ctx context.Context) ([]string, error) {
	output, err := runDockerCommand(ctx, 15*time.Second, "ps", "-a", "--filter", "label=onlava.substrate=neon", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func removeOnlavaNeonContainers(ctx context.Context) error {
	names, err := onlavaNeonContainerNames(ctx)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"rm", "-f", "-v"}, names...)
	_, err = runDockerCommand(ctx, 90*time.Second, args...)
	return err
}

func markImagesUnknown(images []neonImageStatus, message string) {
	for i := range images {
		images[i].Status = "unknown"
		images[i].Message = message
	}
}

func markComponentsNotStarted(components []neonComponentStatus, message string) {
	for i := range components {
		components[i].Status = "not_started"
		components[i].Message = message
	}
}

func generatedFilesMissing(files []neonGeneratedFile) bool {
	for _, file := range files {
		if file.Status != "present" {
			return true
		}
	}
	return false
}

func componentsAllRunning(components []neonComponentStatus) bool {
	if len(components) == 0 {
		return false
	}
	for _, component := range components {
		if component.Status != "running" {
			return false
		}
	}
	return true
}

func componentsPartiallyRunning(components []neonComponentStatus) bool {
	for _, component := range components {
		if component.Status == "running" {
			return true
		}
	}
	return false
}

func componentStatusesInclude(components []neonComponentStatus, status string) bool {
	for _, component := range components {
		if component.Status == status {
			return true
		}
	}
	return false
}

func fileStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "present"
	}
	return "missing"
}

func dbBranchCommand(args []string) error {
	return runDBBranchCommand(context.Background(), os.Stdout, args)
}

func runDBBranchCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseDBBranchArgs(args)
	if err != nil {
		return err
	}
	switch opts.Command {
	case "status":
		return runDBBranchStatus(ctx, stdout, opts)
	case "list":
		return runDBBranchList(ctx, stdout, opts)
	case "checkout":
		return runDBBranchCheckout(ctx, stdout, opts)
	case "reset":
		return runDBBranchReset(ctx, stdout, opts)
	case "delete":
		return runDBBranchDelete(ctx, stdout, opts)
	case "restore":
		return runDBBranchRestore(ctx, stdout, opts)
	case "diff":
		return runDBBranchDiff(ctx, stdout, opts)
	case "expire":
		return runDBBranchExpire(ctx, stdout, opts)
	case "prune":
		return runDBBranchPrune(ctx, stdout, opts)
	default:
		return fmt.Errorf("db branch %s is not implemented yet", opts.Command)
	}
}

func parseDBBranchArgs(args []string) (dbBranchOptions, error) {
	if len(args) == 0 {
		return dbBranchOptions{}, fmt.Errorf("usage: onlava db branch status|list|checkout|reset|delete|restore|diff|expire|prune [--json] [--app-root <path>]")
	}
	opts := dbBranchOptions{Command: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--yes":
			opts.Yes = true
		case "--force":
			opts.Force = true
		case "--at":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --at")
			}
			opts.At = args[i]
		case "--after":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --after")
			}
			opts.After = args[i]
		case "--older-than":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --older-than")
			}
			opts.Older = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return dbBranchOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			if opts.Branch == "" {
				opts.Branch = args[i]
			} else if opts.Target == "" {
				opts.Target = args[i]
			} else {
				return dbBranchOptions{}, fmt.Errorf("unexpected argument %q", args[i])
			}
		}
	}
	switch opts.Command {
	case "status", "list", "checkout", "reset", "delete", "prune", "restore", "diff", "expire":
	default:
		return dbBranchOptions{}, fmt.Errorf("unknown db branch command %q", opts.Command)
	}
	return opts, nil
}

func runDBBranchStatus(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	result, err := buildDBBranchStatus(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	if result.Pin != nil {
		fmt.Fprintf(stdout, "db branch %s (%s)\n", result.Pin.Branch, result.Pin.BranchID)
		return nil
	}
	fmt.Fprintf(stdout, "db branch %s\n", result.Status)
	return nil
}

func runDBBranchList(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	status, err := buildDBBranchStatus(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	registry, registryPath, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		return err
	}
	result := dbBranchListResult{
		SchemaVersion: dbBranchListSchemaVersion,
		OK:            true,
		App:           status.App,
		Provider:      status.Provider,
		Branches:      []worktreeDBPin{},
		RegistryPath:  registryPath,
	}
	provider := neonBranchProviderForConfig(cfg)
	seen := map[string]bool{}
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			continue
		}
		if lease.Pin.Project != sanitizeNeonBranchSegment(firstNonEmpty(neonPostgresService(cfg).Project, cfg.AppID(), "app")) {
			continue
		}
		result.Branches = append(result.Branches, lease.Pin)
		result.Leases = append(result.Leases, dbBranchListLeaseFromRegistryLease(ctx, provider, lease))
		seen[lease.Pin.BranchID] = true
	}
	if status.Pin != nil && !seen[status.Pin.BranchID] {
		result.Branches = append(result.Branches, *status.Pin)
		result.Leases = append(result.Leases, dbBranchListLease{
			Pin:      *status.Pin,
			Status:   firstNonEmpty(status.BackendStatus, "missing"),
			Endpoint: cloneNeonEndpoint(status.Connection),
		})
	}
	if len(result.Branches) == 0 {
		result.Message = "No Onlava-owned Neon branch leases exist for this app."
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	for _, branch := range result.Branches {
		fmt.Fprintf(stdout, "%s %s\n", branch.Branch, branch.BranchID)
	}
	return nil
}

func runDBBranchCheckout(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		return fmt.Errorf("usage: onlava db branch checkout <name> [--app-root <path>] [--json]")
	}
	pin, err := buildWorktreeDBPin(appRoot, cfg, branch)
	if err != nil {
		return err
	}
	if err := writeWorktreeDBPin(appRoot, pin); err != nil {
		return err
	}
	if _, err := neonBranchProviderForConfig(cfg).EnsureBranch(ctx, pin); err != nil {
		return err
	}
	result, err := buildDBBranchStatus(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	result.Message = "Current worktree database branch pin updated. Neon branch provider ensure ran; connection becomes usable when backend_status is ready."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "checked out db branch %s (%s)\n", pin.Branch, pin.BranchID)
	return nil
}

func runDBBranchExpire(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.After) == "" {
		return fmt.Errorf("onlava db branch expire requires --after <duration>")
	}
	after, err := time.ParseDuration(strings.TrimSpace(opts.After))
	if err != nil {
		return fmt.Errorf("parse --after: %w", err)
	}
	target, err := resolveBranchCommandTarget(appRoot, cfg, opts)
	if err != nil {
		return err
	}
	if err := expireNeonBranchLease(target, time.Now().UTC().Add(after)); err != nil {
		return err
	}
	result, err := buildDBBranchStatus(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	result.Message = fmt.Sprintf("Local Neon branch lease %q expiration updated. Backend expiration is not implemented yet.", target.Branch)
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "updated db branch lease expiration for %s\n", target.Branch)
	return nil
}

func runDBBranchPrune(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	var olderThan time.Duration
	if strings.TrimSpace(opts.Older) != "" {
		olderThan, err = time.ParseDuration(strings.TrimSpace(opts.Older))
		if err != nil {
			return fmt.Errorf("parse --older-than: %w", err)
		}
	}
	current, _, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return err
	}
	project := sanitizeNeonBranchSegment(firstNonEmpty(neonPostgresService(cfg).Project, cfg.AppID(), "app"))
	pruned, err := pruneExpiredNeonBranchLeases(project, current.BranchID, olderThan)
	if err != nil {
		return err
	}
	status, err := buildDBBranchStatus(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	registry, registryPath, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		return err
	}
	result := dbBranchListResult{
		SchemaVersion: dbBranchListSchemaVersion,
		OK:            true,
		App:           status.App,
		Provider:      status.Provider,
		Branches:      registryPins(registry, cfg),
		Leases:        registryListLeases(ctx, registry, cfg),
		RegistryPath:  registryPath,
		Message:       fmt.Sprintf("Pruned %d expired local Neon branch lease(s). Backend branch deletion is not implemented yet.", pruned),
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "pruned %d expired db branch lease(s)\n", pruned)
	return nil
}

func runDBBranchReset(ctx context.Context, _ io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no worktree database branch pin exists; run `onlava db branch checkout <name>` first")
	}
	if isProtectedNeonParentBranch(pin) {
		return fmt.Errorf("refusing to reset protected parent branch %q", pin.Branch)
	}
	if !opts.Yes {
		return fmt.Errorf("onlava db branch reset requires --yes")
	}
	return neonBranchProviderForConfig(cfg).ResetBranch(ctx, pin, opts)
}

func runDBBranchDelete(ctx context.Context, _ io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	opts.AppRoot = appRoot
	branch := normalizeNeonBranchName(opts.Branch)
	if branch == "" {
		return fmt.Errorf("usage: onlava db branch delete <name> [--app-root <path>] [--force]")
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return err
	}
	targetPin := pin
	if !ok {
		targetPin, err = buildWorktreeDBPin(appRoot, cfg, branch)
		if err != nil {
			return err
		}
	}
	if branch == targetPin.ParentBranch {
		return fmt.Errorf("refusing to delete protected parent branch %q", branch)
	}
	if ok && branch == pin.Branch && !opts.Force {
		return fmt.Errorf("refusing to delete current branch %q without --force", branch)
	}
	return neonBranchProviderForConfig(cfg).DeleteBranch(ctx, targetPin, branch, opts)
}

func buildDBBranchStatus(ctx context.Context, appRoot string, cfg appcfg.Config) (dbBranchStatusResult, error) {
	pinPath := worktreeDBPinPath(appRoot)
	pin, ok, err := readWorktreeDBPin(pinPath)
	if err != nil {
		return dbBranchStatusResult{}, err
	}
	status := "unpinned"
	var pinPtr *worktreeDBPin
	backendStatus := neonBranchBackendStatus{Status: "none"}
	if ok {
		status = "pinned"
		pinPtr = &pin
		backendStatus = neonBranchProviderForConfig(cfg).InspectBranch(ctx, pin)
	}
	return dbBranchStatusResult{
		SchemaVersion:  dbBranchStatusSchemaVersion,
		OK:             true,
		App:            inspectAppRef(appRoot, cfg),
		Provider:       neonSelfhostProvider,
		Status:         status,
		BackendStatus:  backendStatus.Status,
		BackendMessage: backendStatus.Message,
		Connection:     backendStatus.Endpoint,
		PinPath:        pinPath,
		Pin:            pinPtr,
		DatabaseURLEnv: neonDatabaseURLEnv(cfg),
		PSQLCommand:    "onlava db psql",
		ResetCommand:   "onlava db branch reset",
		Message:        dbBranchStatusMessage(ok),
	}, nil
}

func dbBranchStatusMessage(pinned bool) string {
	if pinned {
		return "Current worktree database branch pin is present."
	}
	return "No worktree database branch pin exists yet; run `onlava db branch checkout <name>` to pin this worktree."
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
