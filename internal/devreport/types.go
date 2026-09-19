package devreport

import (
	"encoding/json"
	"time"
)

type TraceSummary struct {
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
	AppID          string    `json:"-"`
	TestTrace      bool      `json:"-"`
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
	AppID       string         `json:"app_id"`
	SessionID   string         `json:"session_id,omitempty"`
	AppRootHash string         `json:"app_root_hash,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	Worktree    string         `json:"worktree,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	SpanID      string         `json:"span_id,omitempty"`
	Level       string         `json:"level"`
	Message     string         `json:"message"`
	Attrs       map[string]any `json:"attrs,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
}

// InternalFailure is the cause behind a report token an application process
// minted. The token went out in a sanitized answer; the cause goes to the
// session's supervisor, which keeps it where the developer can read it.
type InternalFailure struct {
	ReportToken string    `json:"report_token"`
	Code        string    `json:"code"`
	Cause       string    `json:"cause"`
	Timestamp   time.Time `json:"timestamp"`
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
	// InternalFailure accompanies Type "internal-failure".
	InternalFailure *InternalFailure `json:"internal_failure,omitempty"`
}
