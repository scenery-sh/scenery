package main

import (
	json "encoding/json"
	localagent "scenery.sh/internal/agent"
	postgresdb "scenery.sh/internal/postgresdb"
	time "time"
)

type detachedDevResult struct {
	cliPayloadIdentity
	Wait           string             `json:"wait"`
	AlreadyRunning bool               `json:"already_running,omitempty"`
	PID            int                `json:"pid"`
	LogPath        string             `json:"log_path,omitempty"`
	AttachCommand  string             `json:"attach_command"`
	DownCommand    string             `json:"down_command"`
	Session        localagent.Session `json:"session"`
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type worktreeCreateResult struct {
	cliPayloadIdentity
	OK          bool   `json:"ok"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	From        string `json:"from,omitempty"`
	NextCommand string `json:"next_command"`
	Message     string `json:"message,omitempty"`
}

type worktreeListResult struct {
	cliPayloadIdentity
	OK        bool             `json:"ok"`
	AppRoot   string           `json:"app_root"`
	Worktrees []worktreeRecord `json:"worktrees"`
}

type worktreeRemoveResult struct {
	cliPayloadIdentity
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
}

type cliTelemetryRecord struct {
	At          time.Time        `json:"at"`
	Command     string           `json:"command"`
	DurationMS  int64            `json:"duration_ms"`
	ExitCode    int              `json:"exit_code"`
	Version     string           `json:"version"`
	Mode        string           `json:"mode"`
	Measurement string           `json:"measurement,omitempty"`
	App         *cliTelemetryApp `json:"app,omitempty"`
}

type dbServerStatusResponse struct {
	cliPayloadIdentity
	AppRoot    string                    `json:"app_root"`
	Scope      string                    `json:"scope"`
	ResourceID string                    `json:"resource_id,omitempty"`
	Retained   bool                      `json:"retained_data"`
	OK         bool                      `json:"ok"`
	Container  string                    `json:"container"`
	Image      string                    `json:"image,omitempty"`
	Status     string                    `json:"status"`
	Port       int                       `json:"port,omitempty"`
	URL        string                    `json:"url,omitempty"`
	Databases  []postgresdb.DatabaseInfo `json:"databases,omitempty"`
	StatePath  string                    `json:"state_path,omitempty"`
	Restore    *dbServerRestoreStatus    `json:"restore,omitempty"`
}

type versionResponse struct {
	cliPayloadIdentity
	Version       string                    `json:"version"`
	Commit        string                    `json:"commit,omitempty"`
	BuiltAt       string                    `json:"built_at,omitempty"`
	GoVersion     string                    `json:"go_version"`
	ModuleVersion string                    `json:"module_version,omitempty"`
	Toolchain     *toolchainManifestVersion `json:"toolchain_manifest,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type worktreeRecord struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	Bare   bool   `json:"bare,omitempty"`
}

type cliTelemetryApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dbServerRestoreStatus struct {
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchivePath   string `json:"archive_path"`
	Mode          string `json:"mode"`
	SQLStarted    bool   `json:"sql_started"`
}

type toolchainManifestVersion struct {
	Kind            string `json:"kind"`
	SchemaRevision  string `json:"schema_revision"`
	SHA256          string `json:"sha256"`
	ArtifactCount   int    `json:"artifact_count"`
	SourceLockCount int    `json:"source_lock_count"`
}
