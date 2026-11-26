package devreport

import (
	"encoding/json"
	"time"
)

const (
	MaxBatchReports  = 64
	MaxBatchBytes    = 1 << 20
	MaxEnvelopeBytes = 64 << 10
	MaxQueuedBytes   = 4 << 20
	// Includes the batch wrapper and the largest uint64 dropped count.
	BatchWrapperBytes = 64
)

// ReportBatch is the current runtime-to-supervisor protocol. Admission bounds
// both record count and encoded bytes; reports are processed in array order.
type ReportBatch struct {
	Reports []ReportEnvelope `json:"reports"`
	Dropped uint64           `json:"dropped,omitempty"`
}

type InvocationCorrelation struct {
	InvocationID string `json:"invocation_id,omitempty"`
	ExecutionID  string `json:"execution_id,omitempty"`
	Module       string `json:"module,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
}

type TraceSummary struct {
	InvocationCorrelation
	ErrorCode      string    `json:"error_code,omitempty"`
	TraceID        string    `json:"trace_id"`
	SpanID         string    `json:"span_id"`
	SessionID      string    `json:"session_id,omitempty"`
	AppRootHash    string    `json:"app_root_hash,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	Worktree       string    `json:"worktree,omitempty"`
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
	// Generation and BuildInputDigest identify the application generation
	// that served the span in a process-linked development runtime.
	Generation       uint64 `json:"generation,omitempty"`
	BuildInputDigest string `json:"build_input_digest,omitempty"`
	AppID            string `json:"-"`
	TestTrace        bool   `json:"-"`
}

type TraceEvent struct {
	TraceID     string          `json:"trace_id"`
	SpanID      string          `json:"span_id"`
	SessionID   string          `json:"session_id,omitempty"`
	AppRootHash string          `json:"app_root_hash,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	Worktree    string          `json:"worktree,omitempty"`
	EventID     uint64          `json:"event_id"`
	EventTime   time.Time       `json:"event_time"`
	AppID       string          `json:"-"`
	Data        json.RawMessage `json:"-"`
	Event       map[string]any  `json:"event"`
}

type LogEvent struct {
	InvocationCorrelation
	Generation   uint64         `json:"generation,omitempty"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Service      string         `json:"service,omitempty"`
	Operation    string         `json:"operation,omitempty"`
	AppID        string         `json:"app_id"`
	SessionID    string         `json:"session_id,omitempty"`
	AppRootHash  string         `json:"app_root_hash,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	Worktree     string         `json:"worktree,omitempty"`
	TraceID      string         `json:"trace_id,omitempty"`
	SpanID       string         `json:"span_id,omitempty"`
	Level        string         `json:"level"`
	Message      string         `json:"message"`
	Attrs        map[string]any `json:"attrs,omitempty"`
	Timestamp    time.Time      `json:"timestamp"`
}

// MetricEvent is an invocation-scoped gauge sample reported by a worker Host.
type MetricEvent struct {
	InvocationCorrelation
	AppID        string    `json:"app_id"`
	SessionID    string    `json:"session_id,omitempty"`
	TraceID      string    `json:"trace_id"`
	SpanID       string    `json:"span_id"`
	ParentSpanID string    `json:"parent_span_id,omitempty"`
	Generation   uint64    `json:"generation"`
	Service      string    `json:"service"`
	Operation    string    `json:"operation"`
	ErrorCode    string    `json:"error_code,omitempty"`
	DurationMS   float64   `json:"duration_ms"`
	Name         string    `json:"name"`
	Value        float64   `json:"value"`
	Unit         string    `json:"unit,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

type ReplayInvocation struct {
	Module           string              `json:"module"`
	Runtime          string              `json:"runtime"`
	Service          string              `json:"service"`
	Operation        string              `json:"operation"`
	Binding          string              `json:"binding,omitempty"`
	Mode             string              `json:"mode"`
	HTTPMethod       string              `json:"http_method,omitempty"`
	HTTPPath         string              `json:"http_path,omitempty"`
	HTTPBody         json.RawMessage     `json:"http_body,omitempty"`
	Headers          map[string][]string `json:"headers,omitempty"`
	Principal        string              `json:"principal,omitempty"`
	TenantID         string              `json:"tenant_id,omitempty"`
	ContractRevision string              `json:"contract_revision"`
	Generation       uint64              `json:"generation"`
	TraceID          string              `json:"trace_id"`
	Input            json.RawMessage     `json:"input,omitempty"`
	InputTruncated   bool                `json:"input_truncated,omitempty"`
}

// InternalFailure is the cause behind a report token an application process
// minted. The token went out in a sanitized answer; the cause goes to the
// session's supervisor, which keeps it where the developer can read it.
type InternalFailure struct {
	ReportToken string            `json:"report_token"`
	Code        string            `json:"code"`
	Cause       string            `json:"cause"`
	Timestamp   time.Time         `json:"timestamp"`
	Invocation  *ReplayInvocation `json:"invocation,omitempty"`
}

type ReportEnvelope struct {
	Type         string        `json:"type"`
	AppID        string        `json:"app_id"`
	SessionID    string        `json:"session_id,omitempty"`
	AppRootHash  string        `json:"app_root_hash,omitempty"`
	Branch       string        `json:"branch,omitempty"`
	Worktree     string        `json:"worktree,omitempty"`
	ReporterPID  int           `json:"reporter_pid,omitempty"`
	TraceSummary *TraceSummary `json:"trace_summary,omitempty"`
	TraceEvent   *TraceEvent   `json:"trace_event,omitempty"`
	LogEvent     *LogEvent     `json:"log_event,omitempty"`
	MetricEvent  *MetricEvent  `json:"metric_event,omitempty"`
	// InternalFailure accompanies Type "internal-failure".
	InternalFailure *InternalFailure `json:"internal_failure,omitempty"`
}
