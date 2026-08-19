package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/durable/store"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

const (
	// MCPToolMaxBytes is the framework ceiling for one generated MCP tool
	// request or result. Individual registrations may choose a lower limit.
	MCPToolMaxBytes int64 = mcpcontract.MaximumInputBytes
)

// MCPToolCallContext is the public runtime name for the provider-neutral
// contract context. Keeping this alias lets generated external applications
// construct calls without importing Scenery's internal contract package.
type MCPToolCallContext = mcpcontract.ToolCallContext

// MCPToolLimits are enforced before generated codecs or handlers run.
type MCPToolLimits struct {
	MaxInputBytes  int64
	MaxResultBytes int64
}

// MCPToolEffect records the compiler's provider-neutral effect hints.
type MCPToolEffect struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

// MCPToolRegistration is emitted by generated composition. Invoke must call
// the generated service adapter (or its durable dispatch path), never a native
// implementation directly.
type MCPToolRegistration struct {
	ID                 string
	Name               string
	AssistantAddress   string
	CapabilityRevision string
	OperationAddress   string
	ExecutionAddress   string
	Policy             *ContractHTTPPolicy
	Limits             MCPToolLimits
	Effect             MCPToolEffect
	Approval           string
	Durable            bool
	DurableService     string
	DurableTask        string
	DecodeInput        func([]byte) (any, error)
	EncodeOutput       func(any) ([]byte, error)
	Invoke             func(context.Context, MCPToolCallContext, any) (any, error)
}

// MCPToolOutcome is the public runtime name for the provider-neutral tool
// outcome. MCPToolResult remains an alias for callers of the initial runtime
// bridge API.
type MCPToolOutcome = mcpcontract.ToolOutcome
type MCPToolResult = mcpcontract.ToolOutcome

// MCPDurableReceipt is the public runtime name for the provider-neutral
// receipt returned by a durable tool.
type MCPDurableReceipt = mcpcontract.DurableReceipt

// MCPDurableStatus is the ownership-filtered durable state view.
type MCPDurableStatus struct {
	Service      string          `json:"service"`
	TaskName     string          `json:"task_name"`
	ExecutionID  string          `json:"execution_id"`
	State        string          `json:"state"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage string          `json:"error,omitempty"`
}

// MCPDurableRequest identifies a receipt and principal for status/cancel.
type MCPDurableRequest struct {
	Principal   string
	Service     string
	TaskName    string
	ExecutionID string
}

// MCPToolDispatcher resolves generated registrations and runs them with the
// same runtime auth, invocation, policy, and cancellation machinery as HTTP or
// internal contract calls.
type MCPToolDispatcher struct{}

// Keep the runtime bridge assignable to the private gateway's provider-neutral
// dispatcher. The public aliases above let generated external applications
// construct calls without importing Scenery's internal contract package.
var _ mcpcontract.ToolDispatcher = MCPToolDispatcher{}

type mcpDurableOwner struct {
	Principal   string
	Service     string
	TaskName    string
	ExecutionID string
}

type mcpDurableOwnerStore struct {
	sync.RWMutex
	values map[string]mcpDurableOwner
}

var mcpDurableOwners = mcpDurableOwnerStore{values: map[string]mcpDurableOwner{}}

func (MCPToolDispatcher) CallTool(ctx context.Context, call MCPToolCallContext, name string, input json.RawMessage) (MCPToolOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	call.Principal = strings.TrimSpace(call.Principal)
	call.AssistantAddress = strings.TrimSpace(call.AssistantAddress)
	if call.Principal == "" {
		return MCPToolOutcome{}, errors.New("permission_denied: MCP principal is required")
	}
	if name == "" {
		return MCPToolOutcome{}, errors.New("invalid_argument: MCP tool name is required")
	}
	registration, err := lookupMCPTool(call.AssistantAddress, name)
	if err != nil {
		return MCPToolOutcome{}, err
	}
	if registration.CapabilityRevision != "" && strings.TrimSpace(call.CapabilityRevision) != registration.CapabilityRevision {
		return MCPToolOutcome{}, errors.New("revision_conflict: MCP capability revision is stale")
	}
	maxInput, maxResult := normalizeMCPToolLimits(registration.Limits)
	if int64(len(input)) > maxInput {
		return MCPToolOutcome{}, fmt.Errorf("invalid_argument: MCP tool input exceeds %d bytes", maxInput)
	}
	if registration.DecodeInput == nil || registration.EncodeOutput == nil || registration.Invoke == nil {
		return MCPToolOutcome{}, fmt.Errorf("capability_unavailable: MCP tool %s has incomplete generated registration", name)
	}
	state := newMCPToolState(call, registration, input)
	ctx = withState(ctx, state)
	ctx = withRuntimeInvocation(ctx, state)
	restore := enterState(state)
	defer restore()

	typed, err := registration.DecodeInput(input)
	if err != nil {
		return MCPToolOutcome{}, fmt.Errorf("invalid_argument: decode MCP tool input: %w", err)
	}
	state.request.Payload = typed
	value, err := InvokeContractPolicy(ctx, registration.Policy, typed, func(callCtx context.Context) (any, error) {
		return registration.Invoke(callCtx, call, typed)
	})
	if err != nil {
		return MCPToolOutcome{}, err
	}
	if receipt, ok := value.(runtimeapi.ExecutionReceipt); ok {
		result := MCPToolOutcome{Outcome: "accepted", Receipt: &MCPDurableReceipt{
			DurableIdentity: receipt.DurableIdentity, ExecutionID: receipt.ExecutionID, AcceptedRevision: receipt.AcceptedRevision,
		}}
		if registration.Durable && registration.DurableService != "" && registration.DurableTask != "" {
			mcpDurableOwners.Store(registration.DurableService, receipt.ExecutionID, mcpDurableOwner{Principal: call.Principal, Service: registration.DurableService, TaskName: registration.DurableTask})
		}
		return result, nil
	}
	encoded, err := registration.EncodeOutput(value)
	if err != nil {
		return MCPToolOutcome{}, ContractSystemError(fmt.Errorf("encode MCP tool output: %w", err))
	}
	if int64(len(encoded)) > maxResult {
		return MCPToolOutcome{}, errors.New("resource_exhausted: MCP tool result exceeds its declared limit")
	}
	kind, outcome, payload, err := contract.DecodeContractOutcomeEnvelope(encoded)
	if err != nil {
		return MCPToolOutcome{}, ContractSystemError(fmt.Errorf("decode MCP tool outcome: %w", err))
	}
	result := MCPToolOutcome{Outcome: outcome}
	switch kind {
	case "result":
		result.Value = payload
	case "error":
		result.Problem = payload
	default:
		return MCPToolOutcome{}, ContractSystemError(fmt.Errorf("decode MCP tool outcome: unsupported kind %q", kind))
	}
	return result, nil
}

// DurableStatus returns state only to the principal that accepted the receipt.
func (MCPToolDispatcher) DurableStatus(ctx context.Context, request MCPDurableRequest) (MCPDurableStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request.Principal = strings.TrimSpace(request.Principal)
	request.Service = strings.TrimSpace(request.Service)
	request.TaskName = strings.TrimSpace(request.TaskName)
	request.ExecutionID = strings.TrimSpace(request.ExecutionID)
	owner, ok := mcpDurableOwners.Load(request.Service, request.ExecutionID)
	if !ok || owner.Principal != request.Principal || owner.TaskName != request.TaskName {
		return MCPDurableStatus{}, errors.New("not_found: durable execution not found")
	}
	db, ok := activeDurableStore(request.Service)
	if !ok {
		return MCPDurableStatus{}, errors.New("capability_unavailable: durable execution store is unavailable")
	}
	job, found, err := db.GetJob(ctx, request.ExecutionID)
	if err != nil {
		return MCPDurableStatus{}, err
	}
	if !found || job.TaskName != request.TaskName {
		return MCPDurableStatus{}, errors.New("not_found: durable execution not found")
	}
	status := MCPDurableStatus{Service: request.Service, TaskName: request.TaskName, ExecutionID: job.ID, State: job.State}
	if job.ResultCodec == "json" && len(job.ResultBlob) > 0 {
		status.Result = append(json.RawMessage(nil), job.ResultBlob...)
	}
	if len(job.ErrorBlob) > 0 {
		status.ErrorMessage = string(job.ErrorBlob)
	}
	return status, nil
}

// CancelDurable cancels only a receipt owned by the principal that accepted it.
func (MCPToolDispatcher) CancelDurable(ctx context.Context, request MCPDurableRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	request.Principal = strings.TrimSpace(request.Principal)
	request.Service = strings.TrimSpace(request.Service)
	request.TaskName = strings.TrimSpace(request.TaskName)
	request.ExecutionID = strings.TrimSpace(request.ExecutionID)
	owner, ok := mcpDurableOwners.Load(request.Service, request.ExecutionID)
	if !ok || owner.Principal != request.Principal || owner.TaskName != request.TaskName {
		return errors.New("not_found: durable execution not found")
	}
	db, ok := activeDurableStore(request.Service)
	if !ok {
		return errors.New("capability_unavailable: durable execution store is unavailable")
	}
	if err := db.CancelJob(ctx, request.ExecutionID); err != nil {
		return err
	}
	return nil
}

// Status exposes the framework-owned durable status shape expected by the
// private MCP gateway. A raw execution ID is never sufficient to read another
// principal's job; the owner map remains the authorization source.
func (dispatcher MCPToolDispatcher) Status(ctx context.Context, call MCPToolCallContext, executionID string) (json.RawMessage, error) {
	principal := strings.TrimSpace(call.Principal)
	executionID = strings.TrimSpace(executionID)
	owner, ok := mcpDurableOwners.Find(principal, executionID)
	if !ok {
		return nil, errors.New("not_found: durable execution not found")
	}
	status, err := dispatcher.DurableStatus(ctx, MCPDurableRequest{Principal: principal, Service: owner.Service, TaskName: owner.TaskName, ExecutionID: executionID})
	if err != nil {
		return nil, err
	}
	payload := mcpcontract.ExecutionStatus{ExecutionID: status.ExecutionID, State: status.State, Outcome: status.Result}
	if status.ErrorMessage != "" {
		payload.Outcome, err = json.Marshal(map[string]string{"error": status.ErrorMessage})
		if err != nil {
			return nil, ContractSystemError(err)
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, ContractSystemError(err)
	}
	return json.RawMessage(encoded), nil
}

// Cancel exposes the framework-owned durable cancel shape expected by the
// private MCP gateway and enforces the same principal ownership check as
// DurableStatus.
func (dispatcher MCPToolDispatcher) Cancel(ctx context.Context, call MCPToolCallContext, executionID string) (json.RawMessage, error) {
	principal := strings.TrimSpace(call.Principal)
	executionID = strings.TrimSpace(executionID)
	owner, ok := mcpDurableOwners.Find(principal, executionID)
	if !ok {
		return nil, errors.New("not_found: durable execution not found")
	}
	if err := dispatcher.CancelDurable(ctx, MCPDurableRequest{Principal: principal, Service: owner.Service, TaskName: owner.TaskName, ExecutionID: executionID}); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(mcpcontract.CancelResult{ExecutionID: executionID, State: "canceled"})
	if err != nil {
		return nil, ContractSystemError(err)
	}
	return json.RawMessage(encoded), nil
}

func lookupMCPTool(assistantAddress, name string) (MCPToolRegistration, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	var match *MCPToolRegistration
	for _, registration := range global.mcpTools {
		if registration.Name != name || assistantAddress != "" && registration.AssistantAddress != assistantAddress {
			continue
		}
		if match != nil {
			return MCPToolRegistration{}, fmt.Errorf("invalid_argument: MCP tool %s is ambiguous", name)
		}
		copy := registration
		match = &copy
	}
	if match == nil {
		return MCPToolRegistration{}, errors.New("not_found: MCP tool not found")
	}
	return *match, nil
}

func normalizeMCPToolLimits(limits MCPToolLimits) (int64, int64) {
	input, result := limits.MaxInputBytes, limits.MaxResultBytes
	if input <= 0 || input > MCPToolMaxBytes {
		input = MCPToolMaxBytes
	}
	if result <= 0 || result > MCPToolMaxBytes {
		result = MCPToolMaxBytes
	}
	return input, result
}

func newMCPToolState(call MCPToolCallContext, registration MCPToolRegistration, input []byte) *requestState {
	started := time.Now()
	requestID := strings.TrimSpace(call.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	traceID := ""
	for _, key := range []string{"trace_id", "traceparent"} {
		if value := strings.TrimSpace(call.TraceContext[key]); value != "" {
			traceID = value
			break
		}
	}
	traceContext := make(map[string]string, len(call.TraceContext))
	maps.Copy(traceContext, call.TraceContext)
	state := &requestState{
		started: started,
		request: shared.Request{
			Type: shared.APICall, Started: started, InvocationID: requestID,
			TraceID: traceID, CallerBinding: registration.ID,
			Service: call.AssistantAddress, Endpoint: registration.Name, Method: "MCP",
			Path:    "mcp://" + call.AssistantAddress + "/" + registration.Name,
			Headers: make(http.Header), Payload: input,
			API:                &shared.APIDesc{Exposed: false, AuthRequired: call.Principal != ""},
			CronIdempotencyKey: strings.TrimSpace(call.IdempotencyKey),
		},
		auth: AuthInfo{UID: call.Principal, Data: map[string]any{
			"assistant_address": call.AssistantAddress, "conversation_digest": call.ConversationDigest, "trace_context": traceContext,
		}},
		logsEnabled: true, traceEnabled: true,
	}
	return state
}

func activeDurableStore(service string) (*store.Store, bool) {
	activeDurableStores.mu.RLock()
	defer activeDurableStores.mu.RUnlock()
	db := activeDurableStores.stores[service]
	return db, db != nil
}

func RegisterMCPTool(registration MCPToolRegistration) error {
	registration.ID = strings.TrimSpace(registration.ID)
	registration.Name = strings.TrimSpace(registration.Name)
	registration.AssistantAddress = strings.TrimSpace(registration.AssistantAddress)
	registration.CapabilityRevision = strings.TrimSpace(registration.CapabilityRevision)
	if registration.ID == "" || registration.Name == "" || registration.AssistantAddress == "" {
		return errors.New("runtime: MCP tool registration requires id, name, and assistant address")
	}
	if registration.DecodeInput == nil || registration.EncodeOutput == nil || registration.Invoke == nil {
		return fmt.Errorf("runtime: MCP tool %s has incomplete generated codecs or invoke path", registration.Name)
	}
	if registration.Durable && (strings.TrimSpace(registration.DurableService) == "" || strings.TrimSpace(registration.DurableTask) == "") {
		return fmt.Errorf("runtime: MCP durable tool %s has no durable service/task identity", registration.Name)
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return fmt.Errorf("runtime: MCP tool %s policy: %w", registration.Name, err)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.mcpTools == nil {
		global.mcpTools = map[string]MCPToolRegistration{}
	}
	if _, exists := global.mcpTools[registration.ID]; exists {
		return fmt.Errorf("runtime: duplicate MCP tool registration %s", registration.ID)
	}
	for _, existing := range global.mcpTools {
		if existing.AssistantAddress == registration.AssistantAddress && existing.Name == registration.Name {
			return fmt.Errorf("runtime: duplicate MCP tool %s for assistant %s", registration.Name, registration.AssistantAddress)
		}
	}
	global.mcpTools[registration.ID] = registration
	return nil
}

func cloneMCPToolRegistrations(values map[string]MCPToolRegistration) map[string]MCPToolRegistration {
	clone := make(map[string]MCPToolRegistration, len(values))
	maps.Copy(clone, values)
	return clone
}

func (owners *mcpDurableOwnerStore) Store(service, executionID string, owner mcpDurableOwner) {
	owners.Lock()
	defer owners.Unlock()
	if owners.values == nil {
		owners.values = map[string]mcpDurableOwner{}
	}
	owner.Service = service
	owner.ExecutionID = executionID
	key := service + "\x00" + executionID
	if _, exists := owners.values[key]; !exists {
		owners.values[key] = owner
	}
}

func (owners *mcpDurableOwnerStore) Load(service, executionID string) (mcpDurableOwner, bool) {
	owners.RLock()
	defer owners.RUnlock()
	owner, ok := owners.values[service+"\x00"+executionID]
	return owner, ok
}

func (owners *mcpDurableOwnerStore) Find(principal, executionID string) (mcpDurableOwner, bool) {
	owners.RLock()
	defer owners.RUnlock()
	var match mcpDurableOwner
	for _, owner := range owners.values {
		if owner.Principal != principal || owner.ExecutionID != executionID {
			continue
		}
		if match.ExecutionID != "" {
			// The same execution ID across services is not a portable receipt
			// identity; fail closed rather than selecting an arbitrary owner.
			return mcpDurableOwner{}, false
		}
		match = owner
	}
	return match, match.ExecutionID != ""
}
