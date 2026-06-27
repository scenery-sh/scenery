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
	Grafana             json.RawMessage
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
	Running             bool              `json:"running"`
	AppID               string            `json:"appID"`
	BaseAppID           string            `json:"baseAppID,omitempty"`
	RuntimeAppID        string            `json:"runtimeAppID,omitempty"`
	SessionID           string            `json:"sessionID,omitempty"`
	AppRoot             string            `json:"appRoot"`
	PID                 string            `json:"pid,omitempty"`
	Meta                json.RawMessage   `json:"meta,omitempty"`
	Addr                string            `json:"addr,omitempty"`
	APIEncoding         json.RawMessage   `json:"apiEncoding,omitempty"`
	Grafana             *GrafanaState     `json:"grafana,omitempty"`
	Routes              map[string]string `json:"routes,omitempty"`
	Aliases             map[string]string `json:"aliases,omitempty"`
	SessionStatus       string            `json:"sessionStatus,omitempty"`
	SessionStatusReason string            `json:"sessionStatusReason,omitempty"`
	Compiling           bool              `json:"compiling"`
	CompileError        string            `json:"compileError,omitempty"`
}

type GrafanaState struct {
	Enabled          bool               `json:"enabled"`
	Available        bool               `json:"available"`
	Status           string             `json:"status"`
	ServerReady      bool               `json:"server_ready,omitempty"`
	DatasourcesReady bool               `json:"datasources_ready,omitempty"`
	DashboardsReady  bool               `json:"dashboards_ready,omitempty"`
	URL              string             `json:"url,omitempty"`
	OverviewURL      string             `json:"overview_url,omitempty"`
	LogsURL          string             `json:"logs_url,omitempty"`
	EndpointURL      string             `json:"endpoint_url,omitempty"`
	ConfigPath       string             `json:"config_path,omitempty"`
	ProvisioningPath string             `json:"provisioning_path,omitempty"`
	DashboardsPath   string             `json:"dashboards_path,omitempty"`
	Datasources      map[string]string  `json:"datasources,omitempty"`
	DatasourceStatus map[string]string  `json:"datasource_status,omitempty"`
	Dashboards       []GrafanaDashboard `json:"dashboards,omitempty"`
	Message          string             `json:"message,omitempty"`
}

type GrafanaDashboard struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
	URL   string `json:"url"`
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

type Notification struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

type TraceSummary = devreport.TraceSummary
type TraceEvent = devreport.TraceEvent
type LogEvent = devreport.LogEvent

type StoredRequest struct {
	ID     string            `json:"id"`
	AppID  string            `json:"-"`
	Title  string            `json:"title"`
	RPC    string            `json:"rpcName"`
	Svc    string            `json:"svcName"`
	Shared bool              `json:"shared"`
	Data   StoredRequestData `json:"data"`
}

type StoredRequestData struct {
	Method     string          `json:"method"`
	PathParams json.RawMessage `json:"pathParams"`
	Payload    json.RawMessage `json:"payload"`
}

type OnboardingState map[string]time.Time

type ReportEnvelope = devreport.ReportEnvelope

type QueryRequest struct {
	Query     string `json:"query"`
	Params    []any  `json:"params"`
	ArrayMode bool   `json:"arrayMode"`
	DbID      string `json:"dbId"`
	AppID     string `json:"appId"`
}

type TransactionRequest struct {
	Queries []struct {
		SQL    string `json:"sql"`
		Params []any  `json:"params"`
	} `json:"queries"`
	DbID  string `json:"dbId"`
	AppID string `json:"appId"`
}

type APICallRequest struct {
	AppID         string          `json:"app_id"`
	Service       string          `json:"service"`
	Endpoint      string          `json:"endpoint"`
	Path          string          `json:"path"`
	Method        string          `json:"method"`
	Payload       json.RawMessage `json:"payload"`
	AuthPayload   json.RawMessage `json:"auth_payload,omitempty"`
	AuthToken     string          `json:"auth_token,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
}
