package agent

import "time"

const (
	SessionSchemaVersion = "onlava.dev.session.v1"
	StateSchemaVersion   = "onlava.agent.state.v1"

	RouteAPI       = "api"
	RouteDashboard = "dashboard"
	RouteMCP       = "mcp"
)

type Backend struct {
	Network string `json:"network"`
	Addr    string `json:"addr"`
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
	AppPID        string             `json:"app_pid,omitempty"`
	Routes        map[string]string  `json:"routes"`
	Backends      map[string]Backend `json:"backends"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type RegisterRequest struct {
	BaseAppID string             `json:"base_app_id"`
	AppRoot   string             `json:"app_root"`
	Branch    string             `json:"branch,omitempty"`
	Status    string             `json:"status,omitempty"`
	OwnerPID  int                `json:"owner_pid,omitempty"`
	AppPID    string             `json:"app_pid,omitempty"`
	Backends  map[string]Backend `json:"backends,omitempty"`
}

type RegisterResponse struct {
	Session Session `json:"session"`
}

type StatusResponse struct {
	Sessions []Session `json:"sessions"`
}

type HealthResponse struct {
	SchemaVersion string `json:"schema_version"`
	PID           int    `json:"pid"`
	SocketPath    string `json:"socket_path"`
	RouterAddr    string `json:"router_addr"`
}

type State struct {
	SchemaVersion string    `json:"schema_version"`
	PID           int       `json:"pid"`
	SocketPath    string    `json:"socket_path"`
	RouterAddr    string    `json:"router_addr"`
	UpdatedAt     time.Time `json:"updated_at"`
}
