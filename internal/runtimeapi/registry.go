package runtimeapi

import (
	"context"
	"time"
)

// ExecutionReceipt is the transport-neutral result of accepting a durable
// execution. StatusURL is absent unless the active profile authorizes polling.
type ExecutionReceipt struct {
	DurableIdentity  string `json:"durable_identity"`
	ExecutionID      string `json:"execution_id"`
	AcceptedRevision string `json:"accepted_revision"`
	StatusURL        string `json:"status_url,omitempty"`
}

type invocationState struct {
	id            string
	principal     string
	tenantID      string
	traceID       string
	deadline      time.Time
	callerBinding string
	executionID   string
	deployment    string
	locale        string
}

// InvocationMetadata is runtime-internal construction data. The package's Go
// internal boundary prevents application modules from importing it and
// manufacturing an inherited principal token.
type InvocationMetadata struct {
	ID            string
	Principal     string
	TenantID      string
	TraceID       string
	Deadline      time.Time
	CallerBinding string
	ExecutionID   string
	Deployment    string
	Locale        string
}

// Invocation is an opaque, runtime-created authority token for inherited
// internal calls. Its state is deliberately private so application code can
// pass but cannot forge one.
type Invocation struct{ state *invocationState }

func NewInvocation(id, principal, tenantID, traceID string, deadline time.Time) Invocation {
	return NewInvocationWithMetadata(InvocationMetadata{ID: id, Principal: principal, TenantID: tenantID, TraceID: traceID, Deadline: deadline})
}

func NewInvocationWithMetadata(metadata InvocationMetadata) Invocation {
	return Invocation{state: &invocationState{
		id: metadata.ID, principal: metadata.Principal, tenantID: metadata.TenantID,
		traceID: metadata.TraceID, deadline: metadata.Deadline.UTC(),
		callerBinding: metadata.CallerBinding, executionID: metadata.ExecutionID,
		deployment: metadata.Deployment, locale: metadata.Locale,
	}}
}

func (invocation Invocation) Valid() bool {
	return invocation.state != nil && invocation.state.id != ""
}
func (invocation Invocation) ID() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.id
}
func (invocation Invocation) Principal() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.principal
}
func (invocation Invocation) TenantID() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.tenantID
}
func (invocation Invocation) TraceID() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.traceID
}
func (invocation Invocation) Deadline() (time.Time, bool) {
	if invocation.state == nil || invocation.state.deadline.IsZero() {
		return time.Time{}, false
	}
	return invocation.state.deadline, true
}
func (invocation Invocation) CallerBinding() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.callerBinding
}
func (invocation Invocation) ExecutionID() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.executionID
}
func (invocation Invocation) Deployment() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.deployment
}
func (invocation Invocation) Locale() string {
	if invocation.state == nil {
		return ""
	}
	return invocation.state.locale
}

type invocationContextKey struct{}

func WithInvocation(ctx context.Context, invocation Invocation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationContextKey{}, invocation)
}

func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	if ctx == nil {
		return Invocation{}, false
	}
	invocation, ok := ctx.Value(invocationContextKey{}).(Invocation)
	return invocation, ok && invocation.Valid()
}

func SameInvocation(left, right Invocation) bool {
	return left.state != nil && left.state == right.state
}

// Registry is kept in the cycle-free runtime ABI package and re-exported as
// scenery.Registry for generated application adapters.
type Registry interface {
	Register(address string, implementation any) error
}
