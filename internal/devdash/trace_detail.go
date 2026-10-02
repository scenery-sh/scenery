package devdash

import "time"

// TraceDetail is the backend-independent development trace read model.
type TraceDetail struct {
	TraceID string            `json:"trace_id"`
	Spans   []TraceSpanDetail `json:"spans"`
}
type TraceSpanDetail struct {
	TraceSummary
	Events []TraceDetailEvent `json:"events"`
}
type TraceDetailEvent struct {
	Time time.Time      `json:"time"`
	Name string         `json:"name"`
	Data map[string]any `json:"data"`
}
