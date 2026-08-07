package assistantruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

// privateEvent is deliberately unexported: the runtime's Client streams the
// private control representation as NDJSON rather than exposing that wire
// type as part of its provider-neutral API.
type privateEvent = assistantcontrol.Event

type fakeConversation struct {
	privateSessionID  string
	continuationToken string
	events            []privateEvent
	runs              map[string]*fakeRun
	sequence          uint64
}

type fakeRun struct {
	id             string
	approvalID     string
	pending        bool
	cancelled      bool
	completed      bool
	capabilityName string
}

// FakeHelper is a deterministic, concurrency-safe helper implementation. It
// keeps durable event history across Crash/Restart, so callers can reconnect
// with StreamEvents without duplicate records.
type FakeHelper struct {
	mu sync.Mutex

	cfg              FakeConfig
	state            RuntimeState
	restarts         uint64
	nextConv         uint64
	nextRun          uint64
	nextID           uint64
	convs            map[string]*fakeConversation
	malformed        []privateEvent
	forceUnavailable bool
	crashHook        func()

	stopOnce atomic.Uint64
}

var _ Client = (*FakeHelper)(nil)

// NewFakeHelper creates a fake helper in stopped state. Call Start before
// invoking conversation methods. A variadic config keeps the zero-value helper
// convenient in focused tests while still allowing one explicit configuration.
func NewFakeHelper(config ...FakeConfig) *FakeHelper {
	cfg := FakeConfig{}
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.AssistantAddress == "" {
		cfg.AssistantAddress = "support"
	}
	if cfg.RuntimeRevision == "" {
		cfg.RuntimeRevision = "runtime-1"
	}
	if cfg.CapabilityRevision == "" {
		cfg.CapabilityRevision = "capability-1"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.TextChunks = append([]string(nil), cfg.TextChunks...)
	cfg.CapabilityInput = append(json.RawMessage(nil), cfg.CapabilityInput...)
	if len(cfg.CapabilityInput) == 0 {
		cfg.CapabilityInput = json.RawMessage(`{}`)
	}
	if !cfg.Available {
		// Available defaults true; SetUnavailable provides the explicit outage
		// transition. A config can opt out using NewUnavailableFakeHelper.
		cfg.Available = true
	}
	return &FakeHelper{cfg: cfg, state: StateStopped, convs: make(map[string]*fakeConversation)}
}

// NewUnavailableFakeHelper constructs a stopped fake that remains unavailable
// after Start until SetAvailable(true) is called.
func NewUnavailableFakeHelper(config ...FakeConfig) *FakeHelper {
	fake := NewFakeHelper(config...)
	fake.cfg.Available = false
	fake.forceUnavailable = true
	return fake
}

func (f *FakeHelper) Start(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureDefaultsLocked()
	if f.state == StateReady {
		return ErrAlreadyStarted
	}
	if f.state == StateCrashed {
		return ErrUnavailable
	}
	if f.cfg.Available {
		f.state = StateReady
	} else {
		f.state = StateUnavailable
	}
	return nil
}

func (f *FakeHelper) ensureDefaultsLocked() {
	if f.cfg.AssistantAddress == "" {
		f.cfg.AssistantAddress = "support"
	}
	if f.cfg.RuntimeRevision == "" {
		f.cfg.RuntimeRevision = "runtime-1"
	}
	if f.cfg.CapabilityRevision == "" {
		f.cfg.CapabilityRevision = "capability-1"
	}
	if f.cfg.Now == nil {
		f.cfg.Now = time.Now
	}
	if len(f.cfg.CapabilityInput) == 0 {
		f.cfg.CapabilityInput = json.RawMessage(`{}`)
	}
	if f.convs == nil {
		f.convs = make(map[string]*fakeConversation)
	}
	// A literal zero-value FakeHelper is useful in tests. NewUnavailableFakeHelper
	// sets Available explicitly after construction, so only the untouched zero
	// config receives the ready default here.
	if !f.forceUnavailable && !f.cfg.Available && f.cfg.Text == "" && len(f.cfg.TextChunks) == 0 && f.cfg.CapabilityName == "" {
		f.cfg.Available = true
	}
}

func (f *FakeHelper) Stop(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	f.state = StateStopped
	f.stopOnce.Add(1)
	f.mu.Unlock()
	return nil
}

// Close satisfies Client and keeps the concrete fake lifecycle methods useful
// in tests that need an explicit context on Stop.
func (f *FakeHelper) Close() error { return f.Stop(context.Background()) }

func (f *FakeHelper) Health(ctx context.Context) (Health, error) {
	if err := contextErr(ctx); err != nil {
		return Health{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	health := Health{Ready: f.state == StateReady, RuntimeRevision: f.cfg.RuntimeRevision, CapabilityRevision: f.cfg.CapabilityRevision, Status: string(f.state)}
	if err := (assistantcontrol.Health{Ready: health.Ready, RuntimeRevision: health.RuntimeRevision, CapabilityRevision: health.CapabilityRevision, Status: health.Status, Detail: health.Detail}).Validate(); err != nil {
		return Health{}, fmt.Errorf("validate helper health: %w", err)
	}
	return health, nil
}

// State reports the lifecycle state without exposing provider process details.
func (f *FakeHelper) State() RuntimeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *FakeHelper) Info(ctx context.Context) (Info, error) {
	if err := contextErr(ctx); err != nil {
		return Info{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	info := Info{Kind: assistantcontrol.RuntimeDescriptorKind, SchemaRevision: assistantcontrol.DescriptorSchemaRevision, AssistantAddress: f.cfg.AssistantAddress, RuntimeRevision: f.cfg.RuntimeRevision, CapabilityRevision: f.cfg.CapabilityRevision, ControlProtocol: assistantcontrol.ControlProtocol, MCPProtocol: assistantcontrol.MCPProtocolVersion}
	if err := (assistantcontrol.RuntimeDescriptor{Kind: info.Kind, SchemaRevision: info.SchemaRevision, AssistantAddress: info.AssistantAddress, RuntimeRevision: info.RuntimeRevision, CapabilityRevision: info.CapabilityRevision, ControlProtocol: info.ControlProtocol, MCPProtocol: info.MCPProtocol}).Validate(); err != nil {
		return Info{}, fmt.Errorf("validate helper descriptor: %w", err)
	}
	return info, nil
}

func (f *FakeHelper) StartConversation(ctx context.Context, request StartRequest) (StartResult, error) {
	if err := contextErr(ctx); err != nil {
		return StartResult{}, err
	}
	req := request.toControl(assistantcontrol.RequestCreateConversation)
	if err := f.readyAndValid(req); err != nil {
		return StartResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextConv++
	conversationID := fmt.Sprintf("conv-%06d", f.nextConv)
	sessionID := fmt.Sprintf("session-%06d", f.nextConv)
	continuation := "continue-" + sessionID
	fakeConv := &fakeConversation{privateSessionID: sessionID, continuationToken: continuation, runs: make(map[string]*fakeRun)}
	f.convs[sessionID] = fakeConv
	runID := f.appendRunLocked(fakeConv, effectiveMessage(req), req.RunID)
	response, err := f.responseLocked(req, assistantcontrol.ResponseConversationCreated, sessionID, runID, continuation, mustJSON(map[string]string{"conversation_id": conversationID}))
	if err != nil {
		return StartResult{}, err
	}
	var payload struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal(response.Data, &payload); err != nil || payload.ConversationID == "" {
		return StartResult{}, ErrMalformedEvent
	}
	return StartResult{ConversationID: payload.ConversationID, PrivateSessionID: response.PrivateSessionID, ContinuationToken: response.ContinuationToken, RunID: response.RunID}, nil
}

func (f *FakeHelper) SendTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if err := contextErr(ctx); err != nil {
		return TurnResult{}, err
	}
	req := request.toControl(assistantcontrol.RequestSendTurn)
	if err := f.readyAndValid(req); err != nil {
		return TurnResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	conv, ok := f.convs[req.PrivateSessionID]
	if !ok {
		return TurnResult{}, ErrConversation
	}
	if err := validateContinuation(req, conv); err != nil {
		return TurnResult{}, err
	}
	runID := f.appendRunLocked(conv, effectiveMessage(req), req.RunID)
	response, err := f.responseLocked(req, assistantcontrol.ResponseTurnAccepted, req.PrivateSessionID, runID, conv.continuationToken, nil)
	if err != nil {
		return TurnResult{}, err
	}
	return TurnResult{PrivateSessionID: response.PrivateSessionID, ContinuationToken: response.ContinuationToken, RunID: response.RunID}, nil
}

// StreamEvents returns a deterministic private NDJSON stream containing events
// strictly greater than After. The fake materializes the bytes synchronously;
// callers can close the returned reader to release it, while future runs remain
// independent of this read operation.
func (f *FakeHelper) StreamEvents(ctx context.Context, request StreamRequest) (io.ReadCloser, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	req := request.toControl(assistantcontrol.RequestResumeEvents)
	if err := f.readyAndValid(req); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	conv, ok := f.convs[req.PrivateSessionID]
	if !ok {
		return nil, ErrConversation
	}
	if err := validateContinuation(req, conv); err != nil {
		return nil, err
	}
	if len(f.malformed) > 0 {
		return nil, ErrMalformedEvent
	}
	var previous uint64
	encoded := bytes.NewBuffer(nil)
	for _, event := range conv.events {
		if err := validateEvent(event); err != nil {
			return nil, err
		}
		if event.Sequence <= previous {
			return nil, ErrMalformedEvent
		}
		previous = event.Sequence
		if event.Sequence <= req.After {
			continue
		}
		data, err := assistantcontrol.MarshalEvent(event)
		if err != nil {
			return nil, fmt.Errorf("marshal private assistant event: %w", err)
		}
		_, _ = encoded.Write(data)
		_ = encoded.WriteByte('\n')
	}
	return io.NopCloser(bytes.NewReader(encoded.Bytes())), nil
}

func (f *FakeHelper) ResolveApproval(ctx context.Context, request ApprovalRequest) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	req := request.toControl(assistantcontrol.RequestResolveApproval)
	if err := f.readyAndValid(req); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	conv, ok := f.convs[req.PrivateSessionID]
	if !ok {
		return ErrConversation
	}
	if err := validateContinuation(req, conv); err != nil {
		return err
	}
	run, ok := conv.runs[req.RunID]
	if !ok || run.approvalID != req.ApprovalID || !run.pending {
		return ErrApproval
	}
	run.pending = false
	if req.Decision == assistantcontrol.DecisionAllow {
		f.appendEventLocked(conv, EventCapabilityStarted, run.id, run.capabilityName, "", map[string]any{"decision": ApprovalAllow})
		f.appendEventLocked(conv, EventCapabilityComplete, run.id, run.capabilityName, "", map[string]any{"ok": true})
		f.appendEventLocked(conv, EventRunCompleted, run.id, "", "", map[string]any{"state": "completed"})
		run.completed = true
	} else {
		f.appendEventLocked(conv, EventRunFailed, run.id, "", "", map[string]any{"code": "approval_denied", "message": "capability approval denied"})
		run.completed = true
	}
	_, err := f.responseLocked(req, assistantcontrol.ResponseApprovalResolved, req.PrivateSessionID, run.id, conv.continuationToken, mustJSON(map[string]string{"approval_id": req.ApprovalID, "decision": req.Decision}))
	return err
}

func (f *FakeHelper) CancelRun(ctx context.Context, request CancelRequest) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	req := request.toControl(assistantcontrol.RequestCancelRun)
	if err := f.readyAndValid(req); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	conv, ok := f.convs[req.PrivateSessionID]
	if !ok {
		return ErrConversation
	}
	if err := validateContinuation(req, conv); err != nil {
		return err
	}
	run, ok := conv.runs[req.RunID]
	if !ok {
		return ErrRun
	}
	if run.cancelled || run.completed {
		return ErrTerminalRun
	}
	run.cancelled = true
	run.pending = false
	f.appendEventLocked(conv, EventRunCancelled, run.id, "", "", map[string]any{"state": "cancelled"})
	_, err := f.responseLocked(req, assistantcontrol.ResponseRunCancelled, req.PrivateSessionID, req.RunID, conv.continuationToken, mustJSON(map[string]string{"state": "cancelled"}))
	return err
}

func (f *FakeHelper) readyAndValid(req assistantcontrol.Request) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	f.mu.Lock()
	state := f.state
	runtimeRevision := f.cfg.RuntimeRevision
	capabilityRevision := f.cfg.CapabilityRevision
	assistantAddress := f.cfg.AssistantAddress
	f.mu.Unlock()
	switch state {
	case StateUnavailable, StateCrashed:
		return ErrUnavailable
	case StateStopped:
		return ErrStopped
	case StateStarting:
		return ErrUnavailable
	}
	if state != StateReady {
		return ErrNotStarted
	}
	if req.RuntimeRevision != runtimeRevision {
		return fmt.Errorf("%w: %w", ErrRevisionMismatch, assistantcontrol.RevisionMismatchError{Field: "runtime_revision", Expected: runtimeRevision, Actual: req.RuntimeRevision})
	}
	if req.CapabilityRevision != capabilityRevision {
		return fmt.Errorf("%w: %w", ErrRevisionMismatch, assistantcontrol.RevisionMismatchError{Field: "capability_revision", Expected: capabilityRevision, Actual: req.CapabilityRevision})
	}
	if req.AssistantAddress != assistantAddress {
		return ErrInvalidRequest
	}
	return nil
}

func validateContinuation(req assistantcontrol.Request, conv *fakeConversation) error {
	if req.PrivateSessionID != conv.privateSessionID || req.ContinuationToken != conv.continuationToken {
		return ErrInvalidRequest
	}
	return nil
}

func effectiveMessage(req assistantcontrol.Request) string {
	if strings.TrimSpace(req.Message) != "" {
		return req.Message
	}
	return string(req.Data)
}

func (f *FakeHelper) appendRunLocked(conv *fakeConversation, text, requestedRunID string) string {
	runID := strings.TrimSpace(requestedRunID)
	if runID == "" {
		f.nextRun++
		runID = fmt.Sprintf("run-%06d", f.nextRun)
	}
	if _, exists := conv.runs[runID]; exists {
		return runID
	}
	run := &fakeRun{id: runID}
	conv.runs[runID] = run
	f.appendEventLocked(conv, EventRunStarted, runID, "", "", map[string]any{"state": "started"})
	chunks := append([]string(nil), f.cfg.TextChunks...)
	if len(chunks) == 0 {
		if f.cfg.Text != "" {
			chunks = []string{f.cfg.Text}
		} else {
			chunks = []string{text}
		}
	}
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		f.appendEventLocked(conv, EventMessageDelta, runID, "", "", map[string]string{"text": chunk})
	}
	f.appendEventLocked(conv, EventMessageCompleted, runID, "", "", map[string]string{"text": strings.Join(chunks, "")})
	if f.cfg.CapabilityName != "" {
		run.capabilityName = f.cfg.CapabilityName
		f.nextID++
		run.approvalID = fmt.Sprintf("approval-%06d", f.nextID)
		if f.cfg.RequireApproval {
			run.pending = true
		}
		f.appendEventLocked(conv, EventCapabilityProposed, runID, f.cfg.CapabilityName, run.approvalID, map[string]any{"name": f.cfg.CapabilityName, "input": json.RawMessage(f.cfg.CapabilityInput)})
		if f.cfg.RequireApproval {
			f.appendEventLocked(conv, EventApprovalRequired, runID, f.cfg.CapabilityName, run.approvalID, map[string]string{"approval_id": run.approvalID})
			return runID
		}
		f.appendEventLocked(conv, EventCapabilityStarted, runID, f.cfg.CapabilityName, "", map[string]any{"decision": ApprovalAllow})
		f.appendEventLocked(conv, EventCapabilityComplete, runID, f.cfg.CapabilityName, "", map[string]any{"ok": true})
	}
	f.appendEventLocked(conv, EventRunCompleted, runID, "", "", map[string]any{"state": "completed"})
	run.completed = true
	return runID
}

func (f *FakeHelper) appendEventLocked(conv *fakeConversation, typ, runID, capability, approval string, data any) {
	if typ == "" {
		return
	}
	encoded := mustJSON(data)
	event := privateEvent{Kind: assistantcontrol.EventKind, SchemaRevision: assistantcontrol.EventSchemaRevision, Type: typ, AssistantAddress: f.cfg.AssistantAddress, RuntimeRevision: f.cfg.RuntimeRevision, CapabilityRevision: f.cfg.CapabilityRevision, PrivateSessionID: conv.privateSessionID, ContinuationToken: conv.continuationToken, RunID: runID, Sequence: conv.sequence + 1, OccurredAt: f.cfg.Now().UTC(), CapabilityName: capability, ApprovalID: approval, Data: encoded}
	if typ == EventCapabilityProposed {
		event.Proposal = &assistantcontrol.CapabilityProposal{ApprovalID: approval, CapabilityName: capability, Input: append(json.RawMessage(nil), f.cfg.CapabilityInput...), RequiresApproval: f.cfg.RequireApproval}
	}
	if typ == EventApprovalRequired {
		event.ApprovalWait = &assistantcontrol.ApprovalWait{ApprovalID: approval}
	}
	if typ == EventRuntimeRestarting {
		code, message := "helper_restarted", "assistant helper restarted"
		if payload, ok := data.(map[string]any); ok {
			if value, ok := payload["code"].(string); ok && value != "" {
				code = value
			}
			if value, ok := payload["message"].(string); ok && value != "" {
				message = value
			}
		}
		event.Crash = &assistantcontrol.CrashSignal{Code: code, Message: message, Restartable: true}
	}
	if err := validateEvent(event); err != nil {
		f.malformed = append(f.malformed, cloneEvent(event))
		return
	}
	conv.sequence = event.Sequence
	conv.events = append(conv.events, event)
}

func (f *FakeHelper) responseLocked(req assistantcontrol.Request, typ, sessionID, runID, continuation string, data json.RawMessage) (assistantcontrol.Response, error) {
	response := assistantcontrol.Response{Kind: assistantcontrol.ResponseKind, SchemaRevision: assistantcontrol.ResponseSchemaRevision, Type: typ, RequestID: req.RequestID, AssistantAddress: f.cfg.AssistantAddress, RuntimeRevision: f.cfg.RuntimeRevision, CapabilityRevision: f.cfg.CapabilityRevision, PrivateSessionID: sessionID, ContinuationToken: continuation, RunID: runID, Data: append(json.RawMessage(nil), data...)}
	if typ == assistantcontrol.ResponseApprovalResolved {
		response.ApprovalID = req.ApprovalID
		response.Decision = req.Decision
	}
	if err := response.Validate(); err != nil {
		return assistantcontrol.Response{}, fmt.Errorf("validate helper response: %w", err)
	}
	return response, nil
}

func validateEvent(event privateEvent) error {
	if err := event.Validate(); err != nil {
		return ErrMalformedEvent
	}
	if event.OccurredAt.IsZero() {
		return ErrMalformedEvent
	}
	return nil
}

func cloneEvent(event privateEvent) privateEvent {
	event.Data = append(json.RawMessage(nil), event.Data...)
	return event
}

func (metadata RequestMetadata) controlBase(typ string) assistantcontrol.Request {
	return assistantcontrol.Request{
		Kind: assistantcontrol.RequestKind, SchemaRevision: assistantcontrol.RequestSchemaRevision,
		Type: typ, RequestID: metadata.RequestID, AssistantAddress: metadata.AssistantAddress,
		RuntimeRevision: metadata.RuntimeRevision, CapabilityRevision: metadata.CapabilityRevision,
		Principal: metadata.Principal, ConversationDigest: metadata.ConversationDigest,
	}
}

func (request StartRequest) toControl(typ string) assistantcontrol.Request {
	control := request.RequestMetadata.controlBase(typ)
	control.RunID = request.RunID
	control.Message = request.Message
	control.Data = append(json.RawMessage(nil), request.Data...)
	return control
}

func (request TurnRequest) toControl(typ string) assistantcontrol.Request {
	control := request.RequestMetadata.controlBase(typ)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.RunID = request.RunID
	control.Message = request.Message
	control.Data = append(json.RawMessage(nil), request.Data...)
	return control
}

func (request StreamRequest) toControl(typ string) assistantcontrol.Request {
	control := request.RequestMetadata.controlBase(typ)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.After = request.After
	return control
}

func (request ApprovalRequest) toControl(typ string) assistantcontrol.Request {
	control := request.RequestMetadata.controlBase(typ)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.RunID = request.RunID
	control.ApprovalID = request.ApprovalID
	control.Decision = request.Decision
	return control
}

func (request CancelRequest) toControl(typ string) assistantcontrol.Request {
	control := request.RequestMetadata.controlBase(typ)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.RunID = request.RunID
	return control
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// SetTextChunks changes future runs only. The slice is copied to preserve
// deterministic behavior if a caller mutates its input after configuration.
func (f *FakeHelper) SetTextChunks(chunks []string) {
	f.mu.Lock()
	f.cfg.TextChunks = append([]string(nil), chunks...)
	f.mu.Unlock()
}

func (f *FakeHelper) SetResponseText(text string) {
	f.mu.Lock()
	f.cfg.Text = text
	f.mu.Unlock()
}

func (f *FakeHelper) SetCapability(name string, input json.RawMessage, requireApproval bool) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	f.mu.Lock()
	f.cfg.CapabilityName, f.cfg.CapabilityInput, f.cfg.RequireApproval = name, append(json.RawMessage(nil), input...), requireApproval
	f.mu.Unlock()
}

func (f *FakeHelper) SetAvailable(available bool) {
	f.mu.Lock()
	f.cfg.Available = available
	if available {
		f.forceUnavailable = false
	}
	if !available && f.state == StateReady {
		f.state = StateUnavailable
	} else if available && f.state == StateUnavailable {
		f.state = StateReady
	}
	f.mu.Unlock()
}

func (f *FakeHelper) SetUnavailable() { f.SetAvailable(false) }

func (f *FakeHelper) Crash() {
	f.mu.Lock()
	if f.state == StateCrashed {
		f.mu.Unlock()
		return
	}
	f.state = StateCrashed
	hook := f.crashHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (f *FakeHelper) setCrashHook(hook func()) {
	f.mu.Lock()
	f.crashHook = hook
	f.mu.Unlock()
}

// Restart preserves conversations and durable event sequences while making the
// helper ready again. It also emits one runtime.restarting event in each
// existing conversation so stream consumers can reconnect deterministically.
func (f *FakeHelper) Restart(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state != StateCrashed && f.state != StateUnavailable && f.state != StateStopped {
		return ErrAlreadyStarted
	}
	f.state = StateReady
	f.cfg.Available = true
	f.restarts++
	for _, conv := range f.convs {
		// Runtime events use a synthetic stable run ID so they remain valid in
		// the same per-conversation sequence without leaking process IDs.
		f.appendEventLocked(conv, EventRuntimeRestarting, "runtime", "", "", map[string]any{"restart": f.restarts})
	}
	return nil
}

func (f *FakeHelper) RestartCount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restarts
}

// InjectMalformedEvent makes the next resume fail closed. The event is not
// returned to callers; it is retained as a diagnostic trigger until cleared.
func (f *FakeHelper) InjectMalformedEvent(event assistantcontrol.Event) {
	f.mu.Lock()
	if event.PrivateSessionID != "" {
		if conv, ok := f.convs[event.PrivateSessionID]; ok {
			conv.events = append(conv.events, cloneEvent(event))
			f.mu.Unlock()
			return
		}
	}
	f.malformed = append(f.malformed, event)
	f.mu.Unlock()
}

func (f *FakeHelper) ClearMalformedEvents() {
	f.mu.Lock()
	f.malformed = nil
	f.mu.Unlock()
}

// ConversationID returns the stable public-facing deterministic ID for the
// private session identifier returned by StartConversation.
func (f *FakeHelper) ConversationID(privateSessionID string) string {
	return "conv-" + strings.TrimPrefix(privateSessionID, "session-")
}
