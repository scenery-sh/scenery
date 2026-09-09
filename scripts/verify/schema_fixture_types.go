package main

import (
	deploydiag "scenery.sh/internal/deploydiag"
	doctor "scenery.sh/internal/doctor"
	"scenery.sh/internal/snapshotarchive"
	time "time"
)

type doctorResponse struct {
	cliPayloadIdentity
	OK          bool               `json:"ok"`
	Summary     doctor.Summary     `json:"summary"`
	Scenery     versionResponse    `json:"scenery"`
	App         *doctor.AppInfo    `json:"app,omitempty"`
	Environment doctor.Environment `json:"environment"`
	Deploy      *doctorDeployInfo  `json:"deploy,omitempty"`
	Checks      []doctor.Check     `json:"checks"`
}

type deployStatusResponse struct {
	cliPayloadIdentity
	Ready              bool                         `json:"ready"`
	ServiceManager     string                       `json:"service_manager,omitempty"`
	RegistryPath       string                       `json:"registry_path"`
	PrivilegedListener edgeStatusPrivilegedListener `json:"privileged_listener"`
	HelperPublic       bool                         `json:"helper_public"`
	Edge               edgeStatusCaddy              `json:"edge"`
	Agent              deployAgentStatus            `json:"agent"`
	AgentSupervisor    deployAgentSupervisorStatus  `json:"agent_supervisor"`
	LaunchAgent        deployLaunchAgentStatus      `json:"launch_agent"`
	ACME               deployACMEStatus             `json:"acme"`
	Targets            []deployTargetStatus         `json:"targets"`
	Diagnostics        []string                     `json:"diagnostics,omitempty"`
	DiagnosticsDetail  *deploydiag.Report           `json:"diagnostics_detail,omitempty"`
}

type snapshotSaveResult struct {
	cliPayloadIdentity
	Archive string                 `json:"archive"`
	App     snapshotAppResult      `json:"app"`
	DB      *snapshotDBResult      `json:"db,omitempty"`
	Storage *snapshotStorageResult `json:"storage,omitempty"`
	Files   int64                  `json:"files"`
	Bytes   int64                  `json:"bytes"`
}

type snapshotLoadResult struct {
	cliPayloadIdentity
	Archive string                 `json:"archive"`
	App     snapshotAppResult      `json:"app"`
	Mode    string                 `json:"mode"`
	DryRun  bool                   `json:"dry_run"`
	DB      *snapshotDBResult      `json:"db,omitempty"`
	Storage *snapshotStorageResult `json:"storage,omitempty"`
}

type snapshotVerifyResult struct {
	cliPayloadIdentity
	Archive   string              `json:"archive"`
	App       snapshotManifestApp `json:"app"`
	CreatedAt time.Time           `json:"created_at"`
	Files     int64               `json:"files"`
	Bytes     int64               `json:"bytes"`
	DB        bool                `json:"db"`
	Storage   bool                `json:"storage"`
}

type snapshotManifest struct {
	Kind           string                   `json:"kind"`
	SchemaRevision string                   `json:"schema_revision"`
	CreatedAt      time.Time                `json:"created_at"`
	App            snapshotManifestApp      `json:"app"`
	DB             *snapshotManifestDB      `json:"db,omitempty"`
	Storage        *snapshotManifestStorage `json:"storage,omitempty"`
	Files          []snapshotManifestFile   `json:"files"`
}

type telemetryResponse struct {
	cliPayloadIdentity
	Query        telemetryQuery              `json:"query"`
	Summary      telemetrySummary            `json:"summary"`
	Apps         []telemetryAppStats         `json:"apps"`
	Commands     []telemetryCommandStats     `json:"commands"`
	Measurements []telemetryMeasurementStats `json:"measurements"`
	Records      []cliTelemetryRecord        `json:"records"`
	Warnings     []string                    `json:"warnings"`
}

const snapshotManifestKind = "scenery.snapshot.manifest"

const snapshotManifestSchemaRevision = snapshotarchive.SchemaRevision

const cliTelemetryPayloadKind = "scenery.telemetry"

const defaultTelemetryLimit = 100

const doctorResultKind = "scenery.doctor.result"

type doctorDeployInfo struct {
	cliPayloadIdentity
	Ready        bool                 `json:"ready"`
	RegistryPath string               `json:"registry_path"`
	Targets      []deployTargetStatus `json:"targets"`
	Diagnostics  deploydiag.Report    `json:"diagnostics"`
}

type edgeStatusPrivilegedListener struct {
	Strategy                 string   `json:"strategy"`
	Installed                bool     `json:"installed"`
	State                    string   `json:"state"`
	PID                      int      `json:"pid,omitempty"`
	Listen                   []string `json:"listen,omitempty"`
	Target                   string   `json:"target,omitempty"`
	TargetPath               string   `json:"target_path,omitempty"`
	TargetPID                int      `json:"target_pid,omitempty"`
	OwnerUID                 int      `json:"owner_uid,omitempty"`
	OwnerGID                 int      `json:"owner_gid,omitempty"`
	Version                  string   `json:"version,omitempty"`
	ContractRevision         string   `json:"contract_revision,omitempty"`
	RequiredForPortlessHTTPS bool     `json:"required_for_portless_https,omitempty"`
	InstallCommand           string   `json:"install_command,omitempty"`
	Message                  string   `json:"message,omitempty"`
}

type edgeStatusCaddy struct {
	Kind        string `json:"kind"`
	State       string `json:"state"`
	PID         int    `json:"pid,omitempty"`
	UID         int    `json:"uid,omitempty"`
	HTTPSListen string `json:"https_listen,omitempty"`
	Upstream    string `json:"upstream,omitempty"`
	AgentRouter string `json:"agent_router,omitempty"`
	Admin       string `json:"admin,omitempty"`
	ConfigPath  string `json:"config_path,omitempty"`
	LogPath     string `json:"log_path,omitempty"`
	Error       string `json:"error,omitempty"`
}

type deployAgentStatus struct {
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
	StatePath  string `json:"state_path"`
	SocketPath string `json:"socket_path"`
	RouterAddr string `json:"router_addr,omitempty"`
	Message    string `json:"message,omitempty"`
}

type deployAgentSupervisorStatus struct {
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Label     string `json:"label"`
	Path      string `json:"path"`
}

type deployLaunchAgentStatus struct {
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	State        string `json:"state,omitempty"`
	LastExitCode *int   `json:"last_exit_code,omitempty"`
	Path         string `json:"path"`
}

type deployACMEStatus struct {
	Email string `json:"email,omitempty"`
	CA    string `json:"ca"`
}

type deployTargetStatus struct {
	Environment  string                       `json:"environment,omitempty"`
	Domain       string                       `json:"domain"`
	AppRoot      string                       `json:"app_root"`
	RootService  string                       `json:"root_service,omitempty"`
	Enabled      bool                         `json:"enabled"`
	LiveSession  bool                         `json:"live_session"`
	SessionID    string                       `json:"session_id,omitempty"`
	CertPresent  bool                         `json:"cert_present"`
	CertNotAfter string                       `json:"cert_not_after,omitempty"`
	Frontends    []deployTargetFrontendStatus `json:"frontends,omitempty"`
	Diagnostics  []string                     `json:"diagnostics,omitempty"`
}

type snapshotAppResult struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Root string `json:"root"`
}

type snapshotDBResult struct {
	Database string `json:"database"`
	Source   string `json:"source"`
	Action   string `json:"action,omitempty"`
}

type snapshotStorageResult struct {
	Scope       map[string]any `json:"scope"`
	Stores      int            `json:"stores"`
	Files       int64          `json:"files"`
	Bytes       int64          `json:"bytes"`
	Conflicts   int64          `json:"conflicts,omitempty"`
	Skipped     int64          `json:"skipped,omitempty"`
	Overwritten int64          `json:"overwritten,omitempty"`
	Cloned      int64          `json:"cloned,omitempty"`
	Copied      int64          `json:"copied,omitempty"`
}

type snapshotManifestApp struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type snapshotManifestDB struct {
	Database   string                   `json:"database"`
	Source     string                   `json:"source"`
	Schemas    []snapshotManifestSchema `json:"schemas"`
	DumpFile   string                   `json:"dump_file"`
	DumpFormat string                   `json:"dump_format"`
}

type snapshotManifestStorage = snapshotarchive.Storage

type snapshotManifestFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type telemetryQuery struct {
	Apps         []string `json:"apps"`
	Commands     []string `json:"commands"`
	Measurements []string `json:"measurements"`
	Since        string   `json:"since,omitempty"`
	Limit        int      `json:"limit"`
}

type telemetrySummary struct {
	telemetryTimingStats
	ReturnedCount     int `json:"returned_count"`
	InvalidCount      int `json:"invalid_record_count"`
	UnattributedCount int `json:"unattributed_count"`
}

type telemetryAppStats struct {
	App cliTelemetryApp `json:"app"`
	telemetryTimingStats
}

type telemetryCommandStats struct {
	Command string `json:"command"`
	telemetryTimingStats
}

type telemetryMeasurementStats struct {
	Measurement string `json:"measurement"`
	telemetryTimingStats
}

type deployTargetFrontendStatus struct {
	Environment   string `json:"environment,omitempty"`
	Name          string `json:"name"`
	Route         string `json:"route"`
	BasePath      string `json:"base_path"`
	Mode          string `json:"mode"`
	ArtifactPath  string `json:"artifact_path,omitempty"`
	ReleaseID     string `json:"release_id,omitempty"`
	EntryDocument bool   `json:"entry_document"`
}

type snapshotManifestSchema struct {
	Service string `json:"service"`
	Schema  string `json:"schema"`
}

type telemetryTimingStats struct {
	Count                 int     `json:"count"`
	SuccessCount          int     `json:"success_count"`
	FailureCount          int     `json:"failure_count"`
	TotalDurationMS       int64   `json:"total_duration_ms"`
	AverageMS             float64 `json:"avg_duration_ms"`
	MinDurationMS         int64   `json:"min_duration_ms"`
	MaxDurationMS         int64   `json:"max_duration_ms"`
	PercentileSampleCount int     `json:"percentile_sample_count"`
	P50DurationMS         int64   `json:"p50_duration_ms"`
	P95DurationMS         int64   `json:"p95_duration_ms"`
}
