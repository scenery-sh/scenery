package runtime

// WorkerTelemetryRecord is a span or log sent in a scoped batch.
type WorkerTelemetryRecord struct {
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Message    string         `json:"message,omitempty"`
	ElapsedNS  int64          `json:"elapsed_ns,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}
