package agent

import "time"

const (
	SessionSchemaVersion   = "scenery.dev.session.v1"
	SubstrateSchemaVersion = "scenery.dev.substrate.v1"
	StateSchemaVersion     = "scenery.agent.state.v1"

	RouteAPI       = "api"
	RouteDashboard = "dashboard"
	RouteGrafana   = "grafana"
	RouteTemporal  = "temporal"

	DefaultRouteBaseDomain = "local.dev"

	SubstrateGrafana  = "grafana"
	SubstrateTemporal = "temporal"
	SubstrateVictoria = "victoria"
	SubstrateZeroFS   = "zerofs"
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
	RecordedAt  time.Time `json:"recorded_at"`
}

type Session struct {
	SchemaVersion  string                `json:"schema_version"`
	SessionID      string                `json:"session_id"`
	BaseAppID      string                `json:"base_app_id"`
	RuntimeAppID   string                `json:"runtime_app_id"`
	RouteNamespace RouteNamespace        `json:"route_namespace"`
	AppRoot        string                `json:"app_root"`
	StateRoot      string                `json:"state_root"`
	Branch         string                `json:"branch,omitempty"`
	Status         string                `json:"status"`
	StatusReason   string                `json:"status_reason,omitempty"`
	OwnerPID       int                   `json:"owner_pid,omitempty"`
	Owner          Owner                 `json:"owner"`
	AppPID         string                `json:"app_pid,omitempty"`
	Processes      map[string]Process    `json:"processes,omitempty"`
	Routes         map[string]string     `json:"routes"`
	Aliases        map[string]string     `json:"aliases,omitempty"`
	AliasConflicts map[string]AliasLease `json:"alias_conflicts,omitempty"`
	Backends       map[string]Backend    `json:"backends"`
	ReportToken    string                `json:"-"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type Process struct {
	PID   int   `json:"pid"`
	Owner Owner `json:"owner"`
}

type RegisterRequest struct {
	BaseAppID      string             `json:"base_app_id"`
	AppRoot        string             `json:"app_root"`
	SessionID      string             `json:"session_id,omitempty"`
	Branch         string             `json:"branch,omitempty"`
	Status         string             `json:"status,omitempty"`
	OwnerPID       int                `json:"owner_pid,omitempty"`
	Owner          Owner              `json:"owner"`
	AppPID         string             `json:"app_pid,omitempty"`
	Processes      map[string]Process `json:"processes,omitempty"`
	Backends       map[string]Backend `json:"backends,omitempty"`
	RouteNamespace RouteNamespace     `json:"route_namespace"`
	ReportToken    string             `json:"report_token,omitempty"`
	ClaimOwner     bool               `json:"claim_owner,omitempty"`
	ClaimAliases   bool               `json:"claim_aliases,omitempty"`
}

type RouteNamespace struct {
	Workspace  string            `json:"workspace,omitempty"`
	BaseDomain string            `json:"base_domain,omitempty"`
	Hosts      map[string]string `json:"hosts,omitempty"`
}

type AliasLease struct {
	Host      string    `json:"host"`
	Route     string    `json:"route"`
	SessionID string    `json:"session_id"`
	AppRoot   string    `json:"app_root"`
	OwnerPID  int       `json:"owner_pid,omitempty"`
	Owner     Owner     `json:"owner"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RegisterResponse struct {
	Session Session `json:"session"`
	Deleted bool    `json:"deleted,omitempty"`
}

type StatusResponse struct {
	Sessions []Session `json:"sessions"`
}

type Substrate struct {
	SchemaVersion  string                    `json:"schema_version"`
	Kind           string                    `json:"kind"`
	Status         string                    `json:"status"`
	OwnerPID       int                       `json:"owner_pid,omitempty"`
	Owner          Owner                     `json:"owner"`
	PIDs           map[string]int            `json:"pids,omitempty"`
	Owners         map[string]Owner          `json:"owners,omitempty"`
	URLs           map[string]string         `json:"urls,omitempty"`
	Endpoints      map[string]string         `json:"endpoints,omitempty"`
	Leases         map[string]SubstrateLease `json:"leases,omitempty"`
	LastExit       *SubstrateExit            `json:"last_exit,omitempty"`
	ComponentExits map[string]SubstrateExit  `json:"component_exits,omitempty"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type UpsertSubstrateRequest struct {
	Kind           string                    `json:"kind"`
	Status         string                    `json:"status,omitempty"`
	OwnerPID       int                       `json:"owner_pid,omitempty"`
	Owner          Owner                     `json:"owner"`
	PIDs           map[string]int            `json:"pids,omitempty"`
	Owners         map[string]Owner          `json:"owners,omitempty"`
	URLs           map[string]string         `json:"urls,omitempty"`
	Endpoints      map[string]string         `json:"endpoints,omitempty"`
	Leases         map[string]SubstrateLease `json:"leases"`
	LastExit       *SubstrateExit            `json:"last_exit,omitempty"`
	ComponentExits map[string]SubstrateExit  `json:"component_exits,omitempty"`
}

type SubstrateLease struct {
	SessionID string    `json:"session_id"`
	AppRoot   string    `json:"app_root,omitempty"`
	Route     string    `json:"route,omitempty"`
	URL       string    `json:"url,omitempty"`
	OwnerPID  int       `json:"owner_pid,omitempty"`
	Owner     Owner     `json:"owner"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SubstrateExit struct {
	Component     string    `json:"component,omitempty"`
	PID           int       `json:"pid,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	ExitedAt      time.Time `json:"exited_at"`
	ExitCode      int       `json:"exit_code"`
	Signal        string    `json:"signal,omitempty"`
	Error         string    `json:"error,omitempty"`
	LogPath       string    `json:"log_path,omitempty"`
	StdoutLogPath string    `json:"stdout_log_path,omitempty"`
	StderrLogPath string    `json:"stderr_log_path,omitempty"`
}

type SubstrateResponse struct {
	Substrate Substrate `json:"substrate"`
}

type SubstratesResponse struct {
	Substrates []Substrate `json:"substrates"`
}

type HealthResponse struct {
	SchemaVersion    string     `json:"schema_version"`
	PID              int        `json:"pid"`
	SocketPath       string     `json:"socket_path"`
	RouterAddr       string     `json:"router_addr"`
	PublicRouterAddr string     `json:"public_router_addr,omitempty"`
	RouterScheme     string     `json:"router_scheme"`
	Edge             *EdgeState `json:"edge,omitempty"`
	DashboardBackend Backend    `json:"dashboard_backend"`
}

type State struct {
	SchemaVersion    string     `json:"schema_version"`
	PID              int        `json:"pid"`
	SocketPath       string     `json:"socket_path"`
	RouterAddr       string     `json:"router_addr"`
	PublicRouterAddr string     `json:"public_router_addr,omitempty"`
	RouterScheme     string     `json:"router_scheme,omitempty"`
	Edge             *EdgeState `json:"edge,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type EdgeState struct {
	SchemaVersion string    `json:"schema_version,omitempty"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	PID           int       `json:"pid,omitempty"`
	PublicAddr    string    `json:"public_addr,omitempty"`
	PublicScheme  string    `json:"public_scheme,omitempty"`
	HTTPSListen   string    `json:"https_listen,omitempty"`
	UpstreamAddr  string    `json:"upstream_addr,omitempty"`
	AdminSocket   string    `json:"admin_socket,omitempty"`
	ConfigPath    string    `json:"config_path,omitempty"`
	LogPath       string    `json:"log_path,omitempty"`
	Error         string    `json:"error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type EdgeTargetState struct {
	SchemaVersion string    `json:"schema_version"`
	Kind          string    `json:"kind"`
	TargetAddr    string    `json:"target_addr"`
	PID           int       `json:"pid"`
	OwnerUID      int       `json:"owner_uid"`
	OwnerGID      int       `json:"owner_gid"`
	ProcessStart  string    `json:"process_start"`
	Executable    string    `json:"executable"`
	UpdatedAt     time.Time `json:"updated_at"`
}
