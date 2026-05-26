package devdash

import (
	"encoding/json"
	"os"
	"time"
)

const (
	DashboardAddr = "127.0.0.1:9401"
	WebSocketPath = "/__onlava"
	ReportPath    = "/__onlava/report"
)

func ListenAddr() string {
	if value := os.Getenv("ONLAVA_DEV_DASHBOARD_ADDR"); value != "" {
		return value
	}
	return DashboardAddr
}

type AppRecord struct {
	ID           string
	Name         string
	Root         string
	ListenAddr   string
	Metadata     json.RawMessage
	APIEncoding  json.RawMessage
	Grafana      json.RawMessage
	Offline      bool
	Running      bool
	Compiling    bool
	CompileError string
	PID          string
	UpdatedAt    time.Time
}

type AppStatus struct {
	Running      bool            `json:"running"`
	AppID        string          `json:"appID"`
	AppRoot      string          `json:"appRoot"`
	PID          string          `json:"pid,omitempty"`
	Meta         json.RawMessage `json:"meta,omitempty"`
	Addr         string          `json:"addr,omitempty"`
	APIEncoding  json.RawMessage `json:"apiEncoding,omitempty"`
	Grafana      *GrafanaState   `json:"grafana,omitempty"`
	Compiling    bool            `json:"compiling"`
	CompileError string          `json:"compileError,omitempty"`
}

type GrafanaState struct {
	Enabled          bool               `json:"enabled"`
	Available        bool               `json:"available"`
	Status           string             `json:"status"`
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
	PID       string    `json:"pid"`
	Stream    string    `json:"stream"`
	Output    []byte    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
}

type Notification struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

type TraceSummary struct {
	TraceID        string    `json:"trace_id"`
	SpanID         string    `json:"span_id"`
	Type           string    `json:"type"`
	IsRoot         bool      `json:"is_root"`
	IsError        bool      `json:"is_error"`
	DeployedCommit string    `json:"deployed_commit,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	DurationNanos  uint64    `json:"duration_nanos"`
	ServiceName    string    `json:"service_name,omitempty"`
	EndpointName   *string   `json:"endpoint_name,omitempty"`
	MessageID      *string   `json:"message_id,omitempty"`
	TestSkipped    *bool     `json:"test_skipped,omitempty"`
	SrcFile        *string   `json:"src_file,omitempty"`
	SrcLine        *uint32   `json:"src_line,omitempty"`
	ParentSpanID   *string   `json:"parent_span_id,omitempty"`
	CallerEventID  *uint64   `json:"caller_event_id,omitempty"`
	AppID          string    `json:"-"`
	TestTrace      bool      `json:"-"`
}

type TraceEvent struct {
	TraceID   string          `json:"trace_id"`
	SpanID    string          `json:"span_id"`
	EventID   uint64          `json:"event_id"`
	EventTime time.Time       `json:"event_time"`
	AppID     string          `json:"-"`
	Data      json.RawMessage `json:"-"`
	Event     map[string]any  `json:"event"`
}

type LogEvent struct {
	AppID     string         `json:"app_id"`
	TraceID   string         `json:"trace_id,omitempty"`
	SpanID    string         `json:"span_id,omitempty"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

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

type ReportEnvelope struct {
	Type         string        `json:"type"`
	AppID        string        `json:"app_id"`
	TraceSummary *TraceSummary `json:"trace_summary,omitempty"`
	TraceEvent   *TraceEvent   `json:"trace_event,omitempty"`
	LogEvent     *LogEvent     `json:"log_event,omitempty"`
}

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
