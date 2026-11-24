package workerabi

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	Version              = 1
	ReadyPath            = "/worker/v1/ready"
	InvokePath           = "/worker/v1/invoke"
	CapabilityPrefix     = "/host/v1/"
	ContractHeader       = "X-Worker-Contract"
	ImplementationHeader = "X-Worker-Implementation"
	ScopeHeader          = "X-Worker-Scope"
	StreamSizeHeader     = "X-Worker-Stream-Size"
	StreamMediaType      = "application/vnd.worker.stream"
	MaxMetadataBytes     = 32 << 20
)

type Identity struct {
	ABI                    int    `json:"abi"`
	Service                string `json:"service"`
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
}

type InvocationContext struct {
	InvocationID     string          `json:"invocation_id"`
	Scope            string          `json:"scope"`
	Generation       uint64          `json:"generation"`
	ContractRevision string          `json:"contract_revision"`
	Principal        string          `json:"principal,omitempty"`
	TenantID         string          `json:"tenant_id,omitempty"`
	Traceparent      string          `json:"traceparent,omitempty"`
	Deadline         *time.Time      `json:"deadline,omitempty"`
	Locale           string          `json:"locale,omitempty"`
	ExecutionID      string          `json:"execution_id,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
}

type Invocation struct {
	Service   string            `json:"service"`
	Operation string            `json:"operation"`
	Binding   string            `json:"binding,omitempty"`
	Mode      string            `json:"mode"`
	Context   InvocationContext `json:"context"`
	Input     json.RawMessage   `json:"input"`
}

const (
	NotSent = "not_sent"
	Unknown = "unknown"
)

// ErrImplementation means an attested worker reported a failed handler.
var ErrImplementation = errors.New("worker implementation failed")

// DeliveryError reports an invocation for which no attested outcome arrived.
type DeliveryError struct {
	Delivery string
	Cause    error
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("worker invocation %s: %v", e.Delivery, e.Cause)
}

func (e *DeliveryError) Unwrap() error { return e.Cause }
