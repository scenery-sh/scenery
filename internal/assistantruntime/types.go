// Package assistantruntime owns the provider-neutral lifecycle and helper
// boundary for Scenery assistants. It deliberately depends only on the
// private assistantcontrol messages; provider adapters live below this
// package and are never imported here.
package assistantruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

// RequestMetadata is the negotiated control identity carried by every typed
// runtime request. The concrete helper turns it into the current private
// control envelope at the protocol boundary; provider-facing code never needs
// to construct that envelope directly.
type RequestMetadata struct {
	RequestID          string
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string
	Principal          string
	ConversationDigest string
}

// StartRequest begins a conversation and its first run.
type StartRequest struct {
	RequestMetadata
	RunID   string
	Message string
	Data    json.RawMessage
}

// StartResult contains the durable private session and public conversation
// identity negotiated by the helper.
type StartResult struct {
	ConversationID    string
	PrivateSessionID  string
	ContinuationToken string
	RunID             string
}

// TurnRequest submits a turn to an existing private session.
type TurnRequest struct {
	RequestMetadata
	PrivateSessionID  string
	ContinuationToken string
	RunID             string
	Message           string
	Data              json.RawMessage
}

// TurnResult identifies the accepted run and its continuation cursor.
type TurnResult struct {
	PrivateSessionID  string
	ContinuationToken string
	RunID             string
}

// StreamRequest resumes the private event stream strictly after After.
type StreamRequest struct {
	RequestMetadata
	PrivateSessionID  string
	ContinuationToken string
	After             uint64
}

// ApprovalRequest resolves one pending capability approval.
type ApprovalRequest struct {
	RequestMetadata
	PrivateSessionID  string
	ContinuationToken string
	RunID             string
	ApprovalID        string
	Decision          string
}

// CancelRequest cancels one non-terminal run.
type CancelRequest struct {
	RequestMetadata
	PrivateSessionID  string
	ContinuationToken string
	RunID             string
}

type Health struct {
	Ready              bool
	RuntimeRevision    string
	CapabilityRevision string
	Status             string
	Detail             string
}

type Info struct {
	Kind               string
	SchemaRevision     string
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string
	ControlProtocol    string
	MCPProtocol        string
}

// Client is the provider-neutral helper lifecycle and conversation boundary.
// Every operation carries a context so shutdown and caller cancellation reach
// the helper promptly.
type Client interface {
	Health(context.Context) (Health, error)
	Info(context.Context) (Info, error)
	StartConversation(context.Context, StartRequest) (StartResult, error)
	SendTurn(context.Context, TurnRequest) (TurnResult, error)
	StreamEvents(context.Context, StreamRequest) (io.ReadCloser, error)
	ResolveApproval(context.Context, ApprovalRequest) error
	CancelRun(context.Context, CancelRequest) error
	Close() error
}

// Launcher starts a provider-neutral helper and returns the process handle
// separately so supervision can observe and stop it without knowing the
// implementation.
type Launcher interface {
	Start(context.Context, LaunchSpec) (Client, Process, error)
}

// Process is the minimal child-process lifecycle observation surface needed by
// supervision. Implementations must not expose provider-specific state here.
type Process interface {
	PID() int
	Wait() error
	Stop(context.Context) error
}

type LaunchSpec struct {
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string
}

// RuntimeState is intentionally small and stable. A crashed or unavailable
// helper is never reported as ready.
type RuntimeState string

const (
	StateStopped     RuntimeState = "stopped"
	StateStarting    RuntimeState = "starting"
	StateReady       RuntimeState = "ready"
	StateUnavailable RuntimeState = "unavailable"
	StateCrashed     RuntimeState = "crashed"
)

var (
	ErrUnavailable      = errors.New("assistant helper unavailable")
	ErrStopped          = errors.New("assistant helper stopped")
	ErrRevisionMismatch = errors.New("assistant helper revision mismatch")
	ErrConversation     = errors.New("assistant conversation not found")
	ErrRun              = errors.New("assistant run not found")
	ErrApproval         = errors.New("assistant approval not found")
	ErrMalformedEvent   = errors.New("malformed private assistant event")
	ErrInvalidRequest   = errors.New("invalid assistant control request")
	ErrAlreadyStarted   = errors.New("assistant helper already started")
	ErrNotStarted       = errors.New("assistant helper not started")
	ErrCancelled        = errors.New("assistant run cancelled")
	ErrTerminalRun      = errors.New("assistant run is terminal")
)

// Error carries a stable machine code while retaining errors.Is compatibility
// with the sentinel above.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Event types accepted by the deterministic fake. The private event catalog
// is deliberately distinct from the public assistantapi event catalog.
const (
	EventRunStarted          = assistantcontrol.EventRunStarted
	EventMessageDelta        = assistantcontrol.EventTextDelta
	EventMessageCompleted    = assistantcontrol.EventMessageCompleted
	EventCapabilityProposed  = assistantcontrol.EventCapabilityProposal
	EventApprovalRequired    = assistantcontrol.EventApprovalWait
	EventCapabilityStarted   = assistantcontrol.EventCapabilityStarted
	EventCapabilityComplete  = assistantcontrol.EventCapabilityCompleted
	EventCapabilityCompleted = assistantcontrol.EventCapabilityCompleted
	EventRunCompleted        = assistantcontrol.EventRunCompleted
	EventRunCancelled        = assistantcontrol.EventRunCancelled
	EventRunFailed           = assistantcontrol.EventRunFailed
	EventRuntimeRestarting   = assistantcontrol.EventRuntimeRestarting
)

// FakeConfig controls deterministic fake-helper behavior. Empty revisions are
// filled with stable defaults, and empty TextChunks use Text as one chunk.
// Emission is synchronous and deterministic; there is intentionally no timing
// knob that could race cancellation or make event history nondeterministic.
type FakeConfig struct {
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string
	Text               string
	TextChunks         []string
	CapabilityName     string
	CapabilityInput    json.RawMessage
	RequireApproval    bool
	Available          bool
	Now                func() time.Time
}

// CapabilityProposal describes the capability a fake run can request. It is
// intentionally provider-neutral and is copied into private event data.
type CapabilityProposal struct {
	Name  string
	Input json.RawMessage
}

// ApprovalDecision values accepted by ResolveApproval. No aliases or implicit
// truthy values are accepted.
const (
	ApprovalAllow = "allow"
	ApprovalDeny  = "deny"
)
