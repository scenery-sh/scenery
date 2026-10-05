// Package devtelemetry defines the private development-host latency event.
package devtelemetry

import "time"

const FirstResponsePrefix = "scenery.generation.first_response "

// Observation binds a captured source snapshot to its build operation.
// It never participates in generation identity or application behavior.
type Observation struct {
	OperationID string    `json:"operation_id"`
	ObservedAt  time.Time `json:"observed_at"`
	Initial     bool      `json:"initial"`
}

// FirstResponse records the first attested response headers of a generation.
// It includes idle time before traffic arrives, not just build/activation time.
type FirstResponse struct {
	Observation
	At                     time.Time `json:"at"`
	Generation             uint64    `json:"generation"`
	ImplementationRevision string    `json:"implementation_revision"`
	BuildInputDigest       string    `json:"build_input_digest"`
	Status                 int       `json:"status"`
}
