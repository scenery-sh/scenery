// Package assistantcontrol owns the provider-neutral private protocol between
// Scenery's Go runtime and a managed assistant helper.
package assistantcontrol

import (
	"encoding/json"
	"time"
)

const (
	RequestKind              = "scenery.assistant.control.request"
	ResponseKind             = "scenery.assistant.control.response"
	EventKind                = "scenery.assistant.control.event"
	RuntimeDescriptorKind    = "scenery.assistant.runtime-descriptor"
	ControlProtocol          = "scenery.assistant.control"
	RequestSchemaRevision    = "sha256:eb03bc81084232c8d046780dd51041a069f8bc8cc5fc979f5a6d7106d17dd953"
	ResponseSchemaRevision   = "sha256:8b0cefb8d74e4097e7c058205e55b26675c671e2922b9df8fcd8a86403390e4d"
	EventSchemaRevision      = "sha256:0a6e5fc603f3bb2ea15292b5910af273887003be5a7ae604745501b831a65df8"
	DescriptorSchemaRevision = "sha256:886be15df5243d0c0e321c738e4807339262f47c09b15df50364ef405b54492e"
)

const (
	RequestCreateConversation = "conversation.create"
	RequestSendTurn           = "conversation.turn"
	RequestResumeEvents       = "events.resume"
	RequestResolveApproval    = "approval.resolve"
	RequestCancelRun          = "run.cancel"
	RequestHealth             = "health"
	RequestInfo               = "info"
	MCPProtocolVersion        = "2025-11-25"
)

const (
	ResponseConversationCreated = "conversation.created"
	ResponseTurnAccepted        = "conversation.turn.accepted"
	ResponseEventsResumed       = "events.resumed"
	ResponseApprovalResolved    = "approval.resolved"
	ResponseRunCancelled        = "run.cancelled"
	ResponseHealth              = "health"
	ResponseInfo                = "info"
	ResponseError               = "error"
)

const (
	EventRunStarted          = "run.started"
	EventTextDelta           = "text.delta"
	EventMessageCompleted    = "message.completed"
	EventCapabilityProposal  = "capability.proposal"
	EventApprovalWait        = "approval.wait"
	EventCapabilityStarted   = "capability.started"
	EventCapabilityCompleted = "capability.completed"
	EventRunCompleted        = "run.completed"
	EventRunFailed           = "run.failed"
	EventRunCancelled        = "run.cancelled"
	EventRuntimeCrashed      = "runtime.crashed"
	EventRuntimeRestarting   = "runtime.restarting"
	EventProtocolMalformed   = "protocol.malformed"
)

type Request struct {
	Kind               string          `json:"kind"`
	SchemaRevision     string          `json:"schema_revision"`
	Type               string          `json:"type"`
	RequestID          string          `json:"request_id"`
	AssistantAddress   string          `json:"assistant_address"`
	RuntimeRevision    string          `json:"runtime_revision"`
	CapabilityRevision string          `json:"capability_revision"`
	Principal          string          `json:"principal"`
	ConversationDigest string          `json:"conversation_digest"`
	PrivateSessionID   string          `json:"private_session_id,omitempty"`
	ContinuationToken  string          `json:"continuation_token,omitempty"`
	RunID              string          `json:"run_id,omitempty"`
	ApprovalID         string          `json:"approval_id,omitempty"`
	Decision           string          `json:"decision,omitempty"`
	After              uint64          `json:"after,omitempty"`
	Message            string          `json:"message,omitempty"`
	Data               json.RawMessage `json:"data,omitempty"`
}

// CreateRequest is the typed payload represented by RequestCreateConversation.
// The envelope remains flat for a compact line-oriented control channel.
type CreateRequest struct {
	Principal          string          `json:"principal"`
	ConversationDigest string          `json:"conversation_digest"`
	Message            string          `json:"message,omitempty"`
	Data               json.RawMessage `json:"data,omitempty"`
}

// TurnRequest is the typed payload represented by RequestSendTurn.
type TurnRequest struct {
	Principal          string          `json:"principal"`
	ConversationDigest string          `json:"conversation_digest"`
	PrivateSessionID   string          `json:"private_session_id"`
	ContinuationToken  string          `json:"continuation_token"`
	RunID              string          `json:"run_id,omitempty"`
	Message            string          `json:"message,omitempty"`
	Data               json.RawMessage `json:"data,omitempty"`
}

// ResumeRequest is the typed payload represented by RequestResumeEvents.
type ResumeRequest struct {
	Principal          string `json:"principal"`
	ConversationDigest string `json:"conversation_digest"`
	PrivateSessionID   string `json:"private_session_id"`
	ContinuationToken  string `json:"continuation_token"`
	After              uint64 `json:"after,omitempty"`
}

// ApprovalRequest is the typed payload represented by RequestResolveApproval.
type ApprovalRequest struct {
	Principal          string `json:"principal"`
	ConversationDigest string `json:"conversation_digest"`
	PrivateSessionID   string `json:"private_session_id"`
	ContinuationToken  string `json:"continuation_token"`
	RunID              string `json:"run_id,omitempty"`
	ApprovalID         string `json:"approval_id"`
	Decision           string `json:"decision"`
}

// CancelRequest is the typed payload represented by RequestCancelRun.
type CancelRequest struct {
	Principal          string `json:"principal"`
	ConversationDigest string `json:"conversation_digest"`
	PrivateSessionID   string `json:"private_session_id"`
	ContinuationToken  string `json:"continuation_token"`
	RunID              string `json:"run_id"`
}

type Response struct {
	Kind               string             `json:"kind"`
	SchemaRevision     string             `json:"schema_revision"`
	Type               string             `json:"type"`
	RequestID          string             `json:"request_id"`
	AssistantAddress   string             `json:"assistant_address"`
	RuntimeRevision    string             `json:"runtime_revision"`
	CapabilityRevision string             `json:"capability_revision"`
	PrivateSessionID   string             `json:"private_session_id,omitempty"`
	ContinuationToken  string             `json:"continuation_token,omitempty"`
	RunID              string             `json:"run_id,omitempty"`
	ApprovalID         string             `json:"approval_id,omitempty"`
	Decision           string             `json:"decision,omitempty"`
	NextSequence       uint64             `json:"next_sequence,omitempty"`
	Events             []Event            `json:"events,omitempty"`
	Health             *Health            `json:"health,omitempty"`
	Descriptor         *RuntimeDescriptor `json:"descriptor,omitempty"`
	Data               json.RawMessage    `json:"data,omitempty"`
	Error              *Error             `json:"error,omitempty"`
}

type Event struct {
	Kind               string              `json:"kind"`
	SchemaRevision     string              `json:"schema_revision"`
	Type               string              `json:"type"`
	AssistantAddress   string              `json:"assistant_address"`
	RuntimeRevision    string              `json:"runtime_revision"`
	CapabilityRevision string              `json:"capability_revision"`
	PrivateSessionID   string              `json:"private_session_id"`
	ContinuationToken  string              `json:"continuation_token,omitempty"`
	RunID              string              `json:"run_id"`
	Sequence           uint64              `json:"sequence"`
	OccurredAt         time.Time           `json:"occurred_at"`
	CapabilityName     string              `json:"capability_name,omitempty"`
	ApprovalID         string              `json:"approval_id,omitempty"`
	Proposal           *CapabilityProposal `json:"capability_proposal,omitempty"`
	ApprovalWait       *ApprovalWait       `json:"approval_wait,omitempty"`
	Crash              *CrashSignal        `json:"crash,omitempty"`
	Malformed          *MalformedSignal    `json:"malformed,omitempty"`
	Data               json.RawMessage     `json:"data"`
}

// CapabilityProposal describes a neutral capability request. Input is opaque
// JSON owned by the private Scenery dispatcher, never a provider object.
type CapabilityProposal struct {
	ApprovalID       string          `json:"approval_id"`
	CapabilityName   string          `json:"capability_name"`
	Input            json.RawMessage `json:"input"`
	RequiresApproval bool            `json:"requires_approval"`
}

// ApprovalWait marks the period during which a proposal is waiting for Go-side
// approval. The helper never decides authorization from this record.
type ApprovalWait struct {
	ApprovalID string `json:"approval_id"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// CrashSignal and MalformedSignal are deterministic fake-helper signals used
// to exercise restart and fail-closed handling without provider details.
type CrashSignal struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Restartable bool   `json:"restartable"`
}

type MalformedSignal struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	ObservedType string `json:"observed_type,omitempty"`
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type Health struct {
	Ready              bool   `json:"ready"`
	RuntimeRevision    string `json:"runtime_revision"`
	CapabilityRevision string `json:"capability_revision"`
	Status             string `json:"status,omitempty"`
	Detail             string `json:"detail,omitempty"`
}

type RuntimeDescriptor struct {
	Kind               string `json:"kind"`
	SchemaRevision     string `json:"schema_revision"`
	AssistantAddress   string `json:"assistant_address"`
	RuntimeRevision    string `json:"runtime_revision"`
	CapabilityRevision string `json:"capability_revision"`
	ControlProtocol    string `json:"control_protocol"`
	MCPProtocol        string `json:"mcp_protocol"`
}
