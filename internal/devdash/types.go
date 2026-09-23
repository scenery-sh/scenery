package devdash

import (
	"encoding/json"
	"time"

	"scenery.sh/internal/devreport"
	"scenery.sh/internal/envpolicy"
)

const (
	DashboardAddr = "127.0.0.1:9401"
	WebSocketPath = "/__scenery"
	ReportPath    = "/__scenery/report"
)

func ListenAddr() string {
	if value := envpolicy.Get("SCENERY_DEV_DASHBOARD_ADDR"); value != "" {
		return value
	}
	return DashboardAddr
}

type AppRecord struct {
	RouteID             string
	ID                  string
	BaseAppID           string
	RuntimeAppID        string
	SessionID           string
	Name                string
	Root                string
	ListenAddr          string
	Metadata            json.RawMessage
	APIEncoding         json.RawMessage
	Routes              map[string]string
	Aliases             map[string]string
	Offline             bool
	Running             bool
	SessionStatus       string
	SessionStatusReason string
	Compiling           bool
	CompileError        string
	PID                 string
	UpdatedAt           time.Time
}

type AppStatus struct {
	Running             bool                `json:"running"`
	AppID               string              `json:"appID"`
	BaseAppID           string              `json:"baseAppID,omitempty"`
	RuntimeAppID        string              `json:"runtimeAppID,omitempty"`
	SessionID           string              `json:"sessionID,omitempty"`
	AppRoot             string              `json:"appRoot"`
	PID                 string              `json:"pid,omitempty"`
	Meta                json.RawMessage     `json:"meta,omitempty"`
	Addr                string              `json:"addr,omitempty"`
	APIEncoding         json.RawMessage     `json:"apiEncoding,omitempty"`
	Observability       *ObservabilityState `json:"observability,omitempty"`
	Routes              map[string]string   `json:"routes,omitempty"`
	Aliases             map[string]string   `json:"aliases,omitempty"`
	SessionStatus       string              `json:"sessionStatus,omitempty"`
	SessionStatusReason string              `json:"sessionStatusReason,omitempty"`
	Compiling           bool                `json:"compiling"`
	CompileError        string              `json:"compileError,omitempty"`
	// ServiceProcesses reports the service processes of a process-model
	// session; it is absent when the application runs as one process.
	ServiceProcesses []ServiceProcess `json:"serviceProcesses,omitempty"`
}

// ServiceProcess is one service process of the published generation.
type ServiceProcess struct {
	Name                   string `json:"name"`
	PID                    string `json:"pid,omitempty"`
	Generation             uint64 `json:"generation,omitempty"`
	ImplementationRevision string `json:"implementationRevision,omitempty"`
	// State is running for a serving instance whose background work is active,
	// degraded for a service whose process stopped and exhausted its restart
	// budget or whose background work activation is not yet confirmed.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type ObservabilityState struct {
	Enabled bool                      `json:"enabled"`
	Backend string                    `json:"backend"`
	Metrics ObservabilityBackendState `json:"metrics"`
	Logs    ObservabilityBackendState `json:"logs"`
	Traces  ObservabilityBackendState `json:"traces"`
	Scope   *ObservabilityScope       `json:"scope,omitempty"`
	Message string                    `json:"message,omitempty"`
}

type ObservabilityBackendState struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
	URL       string `json:"url,omitempty"`
	QueryPath string `json:"query_path,omitempty"`
	Dialect   string `json:"dialect,omitempty"`
	Message   string `json:"message,omitempty"`
}

type ObservabilityScope struct {
	AppID       string `json:"app_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AppRootHash string `json:"app_root_hash,omitempty"`
	Worktree    string `json:"worktree,omitempty"`
	Branch      string `json:"branch,omitempty"`
}

type ProcessOutput struct {
	ID        int64     `json:"id"`
	AppID     string    `json:"appID"`
	SessionID string    `json:"session_id,omitempty"`
	PID       string    `json:"pid"`
	Stream    string    `json:"stream"`
	Output    []byte    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
}

type DevSource struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role,omitempty"`
	PID       string `json:"pid,omitempty"`
	Stream    string `json:"stream,omitempty"`
	RestartID string `json:"restart_id,omitempty"`
	Status    string `json:"status,omitempty"`
	URL       string `json:"url,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type DevEventParse struct {
	Format string `json:"format"`
	OK     bool   `json:"ok"`
}

type DevEvent struct {
	ID        int64           `json:"id"`
	AppID     string          `json:"-"`
	AppRoot   string          `json:"-"`
	SessionID string          `json:"session_id,omitempty"`
	Source    DevSource       `json:"source"`
	Level     string          `json:"level"`
	Message   string          `json:"message"`
	Fields    json.RawMessage `json:"fields,omitempty"`
	Raw       string          `json:"raw,omitempty"`
	Parse     DevEventParse   `json:"parse"`
	CreatedAt time.Time       `json:"-"`
}

type DevEventQuery struct {
	AppID     string
	SessionID string
	SourceID  string
	Kind      string
	Level     string
	Stream    string
	Grep      string
	Since     time.Time
	AfterID   int64
	Limit     int
}

type TraceSummary = devreport.TraceSummary
type TraceEvent = devreport.TraceEvent
type LogEvent = devreport.LogEvent

type ReportEnvelope = devreport.ReportEnvelope
