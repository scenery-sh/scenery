// Package mcpcontract owns Scenery's provider-neutral MCP capability manifest
// and the wire values exchanged across the private gateway dispatch boundary.
package mcpcontract

import (
	"context"
	"encoding/json"

	"scenery.sh/internal/spec"
)

const (
	ManifestKind             = "scenery.mcp-capability-manifest"
	manifestSchemaDescriptor = `{"kind":"scenery.mcp-capability-manifest","schema_revision":"digest","protocol_version":"2025-11-25","source_revision":"optional_digest","contract_revision":"digest","implementation_revision":"optional_digest","capabilities":"array<capability{id,name,title,description,input_schema,output_schema,operation_address,execution_address,origin,auth,limits,effect,approval,durable,allow_sensitive_output}>","connections":"array<connection{address,namespace,required,allow,block}>"}`
	ProtocolVersion          = "2025-11-25"
	StatusToolName           = "scenery_execution_status"
	CancelToolName           = "scenery_execution_cancel"
	ToolNamePattern          = `^[a-z][a-z0-9_]{0,127}$`
	MaximumInputBytes        = 16 << 20
	MaximumResultBytes       = 16 << 20
)

var ManifestSchemaRevision = string(spec.SchemaRevision(manifestSchemaDescriptor))

type Manifest struct {
	Kind                   string       `json:"kind"`
	SchemaRevision         string       `json:"schema_revision"`
	ProtocolVersion        string       `json:"protocol_version"`
	SourceRevision         string       `json:"source_revision,omitempty"`
	ContractRevision       string       `json:"contract_revision"`
	ImplementationRevision string       `json:"implementation_revision,omitempty"`
	Capabilities           []Capability `json:"capabilities"`
	Connections            []Connection `json:"connections"`
}

type Capability struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Title                string          `json:"title,omitempty"`
	Description          string          `json:"description,omitempty"`
	InputSchema          json.RawMessage `json:"input_schema"`
	OutputSchema         json.RawMessage `json:"output_schema"`
	OperationAddress     string          `json:"operation_address"`
	ExecutionAddress     string          `json:"execution_address"`
	Origin               Origin          `json:"origin"`
	Auth                 Auth            `json:"auth"`
	Limits               Limits          `json:"limits"`
	Effect               Effect          `json:"effect"`
	Approval             Approval        `json:"approval"`
	Durable              bool            `json:"durable"`
	AllowSensitiveOutput bool            `json:"allow_sensitive_output"`
}

// Connection identifies a federated capability source without carrying its
// URL, authentication material, or any provider-specific configuration.
type Connection struct {
	Address   string   `json:"address"`
	Namespace string   `json:"namespace"`
	Required  bool     `json:"required"`
	Allow     []string `json:"allow,omitempty"`
	Block     []string `json:"block,omitempty"`
}

type Origin struct {
	Kind      string `json:"kind"`
	Address   string `json:"address"`
	Namespace string `json:"namespace,omitempty"`
}

type Auth struct {
	Authentication string `json:"authentication"`
	Authorization  string `json:"authorization"`
}

type Limits struct {
	MaxInputBytes  int `json:"max_input_bytes"`
	MaxResultBytes int `json:"max_result_bytes"`
}

type Effect struct {
	ReadOnly    bool `json:"read_only"`
	Destructive bool `json:"destructive"`
	Idempotent  bool `json:"idempotent"`
	OpenWorld   bool `json:"open_world"`
}

type Approval string

const (
	ApprovalNever  Approval = "never"
	ApprovalAlways Approval = "always"
)

// ToolCallContext is derived from a short-lived private assertion. Principal
// is a Scenery identity, never an application bearer token or external secret.
type ToolCallContext struct {
	Principal          string            `json:"principal"`
	AssistantAddress   string            `json:"assistant_address"`
	ConversationDigest string            `json:"conversation_digest"`
	CapabilityRevision string            `json:"capability_revision"`
	RequestID          string            `json:"request_id"`
	TraceContext       map[string]string `json:"trace_context,omitempty"`
	IdempotencyKey     string            `json:"idempotency_key,omitempty"`
}

type ToolOutcome struct {
	Outcome string          `json:"outcome"`
	Value   json.RawMessage `json:"value,omitempty"`
	Problem json.RawMessage `json:"problem,omitempty"`
	Receipt *DurableReceipt `json:"receipt,omitempty"`
}

type DeclaredProblem struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Path    string          `json:"path,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

type DurableReceipt struct {
	DurableIdentity  string `json:"durable_identity"`
	ExecutionID      string `json:"execution_id"`
	AcceptedRevision string `json:"accepted_revision"`
}

type ExecutionStatus struct {
	ExecutionID string          `json:"execution_id"`
	State       string          `json:"state"`
	Outcome     json.RawMessage `json:"outcome,omitempty"`
}

type CancelResult struct {
	ExecutionID string `json:"execution_id"`
	State       string `json:"state"`
}

type ToolDispatcher interface {
	CallTool(context.Context, ToolCallContext, string, json.RawMessage) (ToolOutcome, error)
}
