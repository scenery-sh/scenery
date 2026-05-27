package agent

import "time"

const (
	SessionSchemaVersion   = "onlava.dev.session.v1"
	SubstrateSchemaVersion = "onlava.dev.substrate.v1"
	StateSchemaVersion     = "onlava.agent.state.v1"

	RouteAPI       = "api"
	RouteDashboard = "dashboard"
	RouteGrafana   = "grafana"
	RouteMCP       = "mcp"
	RouteTemporal  = "temporal"

	SubstrateGrafana  = "grafana"
	SubstratePostgres = "postgres"
	SubstrateTemporal = "temporal"
	SubstrateVictoria = "victoria"
)

type Backend struct {
	Network string `json:"network"`
	Addr    string `json:"addr"`
}

type Owner struct {
	PID         int       `json:"pid,omitempty"`
	StartedAt   string    `json:"started_at,omitempty"`
	Exe         string    `json:"exe,omitempty"`
	CmdlineHash string    `json:"cmdline_hash,omitempty"`
	AgentPID    int       `json:"agent_pid,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
	RecordedAt  time.Time `json:"recorded_at,omitempty"`
}

type Session struct {
	SchemaVersion string             `json:"schema_version"`
	SessionID     string             `json:"session_id"`
	BaseAppID     string             `json:"base_app_id"`
	RuntimeAppID  string             `json:"runtime_app_id"`
	AppRoot       string             `json:"app_root"`
	StateRoot     string             `json:"state_root"`
	Branch        string             `json:"branch,omitempty"`
	Status        string             `json:"status"`
	OwnerPID      int                `json:"owner_pid,omitempty"`
	Owner         Owner              `json:"owner,omitempty"`
	AppPID        string             `json:"app_pid,omitempty"`
	Routes        map[string]string  `json:"routes"`
	Backends      map[string]Backend `json:"backends"`
	ReportToken   string             `json:"-"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type RegisterRequest struct {
	BaseAppID   string             `json:"base_app_id"`
	AppRoot     string             `json:"app_root"`
	SessionID   string             `json:"session_id,omitempty"`
	Branch      string             `json:"branch,omitempty"`
	Status      string             `json:"status,omitempty"`
	OwnerPID    int                `json:"owner_pid,omitempty"`
	Owner       Owner              `json:"owner,omitempty"`
	AppPID      string             `json:"app_pid,omitempty"`
	Backends    map[string]Backend `json:"backends,omitempty"`
	ReportToken string             `json:"report_token,omitempty"`
}

type RegisterResponse struct {
	Session Session `json:"session"`
}

type StatusResponse struct {
	Sessions []Session `json:"sessions"`
}

type Substrate struct {
	SchemaVersion string            `json:"schema_version"`
	Kind          string            `json:"kind"`
	Status        string            `json:"status"`
	OwnerPID      int               `json:"owner_pid,omitempty"`
	Owner         Owner             `json:"owner,omitempty"`
	PIDs          map[string]int    `json:"pids,omitempty"`
	Owners        map[string]Owner  `json:"owners,omitempty"`
	URLs          map[string]string `json:"urls,omitempty"`
	Endpoints     map[string]string `json:"endpoints,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type UpsertSubstrateRequest struct {
	Kind      string            `json:"kind"`
	Status    string            `json:"status,omitempty"`
	OwnerPID  int               `json:"owner_pid,omitempty"`
	Owner     Owner             `json:"owner,omitempty"`
	PIDs      map[string]int    `json:"pids,omitempty"`
	Owners    map[string]Owner  `json:"owners,omitempty"`
	URLs      map[string]string `json:"urls,omitempty"`
	Endpoints map[string]string `json:"endpoints,omitempty"`
}

type SubstrateResponse struct {
	Substrate Substrate `json:"substrate"`
}

type SubstratesResponse struct {
	Substrates []Substrate `json:"substrates"`
}

type HealthResponse struct {
	SchemaVersion string `json:"schema_version"`
	PID           int    `json:"pid"`
	SocketPath    string `json:"socket_path"`
	RouterAddr    string `json:"router_addr"`
	RouterScheme  string `json:"router_scheme"`
}

type State struct {
	SchemaVersion string    `json:"schema_version"`
	PID           int       `json:"pid"`
	SocketPath    string    `json:"socket_path"`
	RouterAddr    string    `json:"router_addr"`
	RouterScheme  string    `json:"router_scheme,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}
