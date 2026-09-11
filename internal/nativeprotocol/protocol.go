// Package nativeprotocol defines the bounded, explicit values crossing the
// plan-0180 experimental kernel/worker boundary. It has no runtime owner.
package nativeprotocol

import (
	"encoding/json"
	"net/http"
	"scenery.sh/internal/authbridge"
	"scenery.sh/runtime/shared"
	"time"
)

const Protocol = "scenery.native-worker-experiment"
const Revision = "sha256:2054a9be33e0886076409d9a8771b1513934ff7783c441c1f761a3da631d5efb"

const MaxMessageBytes = 8 << 20

type InvocationRequest struct {
	ProtocolRevision string          `json:"protocol_revision"`
	Protocol         string          `json:"protocol"`
	ContractRevision string          `json:"contract_revision"`
	InputDigest      string          `json:"input_digest"`
	Operation        string          `json:"operation"`
	Context          RequestContext  `json:"context"`
	Input            json.RawMessage `json:"input"`
}

type InvocationResponse struct {
	Outcome json.RawMessage `json:"outcome,omitempty"`
	Error   string          `json:"error,omitempty"`
	Spans   []SpanRecord    `json:"spans,omitempty"`
}

// RequestContext is an explicit transport value. It deliberately cannot carry
// arbitrary context values, native errors, SQL handles, or Go callbacks.
type RequestContext struct {
	Started      time.Time         `json:"started"`
	Headers      http.Header       `json:"headers"`
	PathParams   shared.PathParams `json:"path_params"`
	ExecutionID  string            `json:"execution_id"`
	Deployment   string            `json:"deployment"`
	Locale       string            `json:"locale"`
	AuthRequired bool              `json:"auth_required"`

	InvocationID  string        `json:"invocation_id"`
	Principal     string        `json:"principal"`
	Auth          *StandardAuth `json:"auth"`
	TraceID       string        `json:"trace_id"`
	CallerBinding string        `json:"caller_binding"`
	Deadline      time.Time     `json:"deadline"`
	Service       string        `json:"service"`
	Endpoint      string        `json:"endpoint"`
	Method        string        `json:"method"`
	Path          string        `json:"path"`
}

type SpanRecord struct {
	Name    string    `json:"name"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
	Failed  bool      `json:"failed"`
}

type StandardAuth = authbridge.StandardIdentity
