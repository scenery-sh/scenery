package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"scenery.sh/errs"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/mcpcontract"
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
// the generated service adapter, never a native
// implementation directly. A tool call reaches it only through an assistant's
// MCP gateway, which admits a call only with the assistant's current capability
// revision, so a registration records no application revision.
type MCPToolRegistration struct {
	ID               string
	Name             string
	AssistantAddress string
	OperationAddress string
	ExecutionAddress string
	Policy           *ContractHTTPPolicy
	Limits           MCPToolLimits
	Effect           MCPToolEffect
	Approval         string
	DecodeInput      func([]byte) (any, error)
	EncodeOutput     func(any) ([]byte, error)
	Invoke           func(context.Context, MCPToolCallContext, any) (any, error)
}

// MCPToolOutcome is the public runtime name for the provider-neutral tool
// outcome.
type MCPToolOutcome = mcpcontract.ToolOutcome

// MCPToolDispatcher resolves generated registrations and runs them with the
// same runtime auth, invocation, policy, and cancellation machinery as HTTP or
// internal contract calls.
type MCPToolDispatcher struct{}

// Keep the runtime bridge assignable to the private gateway's provider-neutral
// dispatcher. The public aliases above let generated external applications
// construct calls without importing Scenery's internal contract package.
var _ mcpcontract.ToolDispatcher = MCPToolDispatcher{}

func (MCPToolDispatcher) CallTool(ctx context.Context, call MCPToolCallContext, name string, input json.RawMessage) (result MCPToolOutcome, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	call.Principal = strings.TrimSpace(call.Principal)
	call.AssistantAddress = strings.TrimSpace(call.AssistantAddress)
	if call.Principal == "" {
		return MCPToolOutcome{}, mcpError(errs.PermissionDenied, "MCP principal is required")
	}
	if name == "" {
		return MCPToolOutcome{}, mcpError(errs.InvalidArgument, "MCP tool name is required")
	}
	registration, err := lookupMCPTool(call.AssistantAddress, name)
	if err != nil {
		return MCPToolOutcome{}, err
	}
	maxInput, maxResult := normalizeMCPToolLimits(registration.Limits)
	if int64(len(input)) > maxInput {
		return MCPToolOutcome{}, mcpError(errs.InvalidArgument, "MCP tool input exceeds %d bytes", maxInput)
	}
	if registration.DecodeInput == nil || registration.EncodeOutput == nil || registration.Invoke == nil {
		return MCPToolOutcome{}, mcpError(errs.Unavailable, "MCP tool %s has incomplete generated registration", name)
	}
	state := newMCPToolState(call, registration, input)
	// A tool call a process host forwarded is pinned to the generation the host
	// selected, and so are the internal calls it makes.
	state.processGeneration, _ = ctx.Value(processGenerationKey{}).(uint64)
	ctx = withState(ctx, state)
	service, attributes := operationTraceIdentity(registration.Policy, call.AssistantAddress, map[string]any{"binding_address": registration.ID})
	ctx, end := beginOperationTrace(ctx, "MCP", service, registration.Name, attributes)
	defer finishOperation(end, &resultErr)
	state = stateFromContext(ctx)
	ctx = withNewRuntimeInvocation(ctx, state)
	restore := enterState(state)
	defer restore()

	typed, err := registration.DecodeInput(input)
	if err != nil {
		return MCPToolOutcome{}, mcpError(errs.InvalidArgument, "decode MCP tool input: %w", err)
	}
	state.request.Payload = typed
	value, err := InvokeContractPolicy(ctx, registration.Policy, typed, func(callCtx context.Context) (any, error) {
		return registration.Invoke(callCtx, call, typed)
	})
	if err != nil {
		return MCPToolOutcome{}, err
	}
	encoded, err := registration.EncodeOutput(value)
	if err != nil {
		return MCPToolOutcome{}, ContractSystemError(fmt.Errorf("encode MCP tool output: %w", err))
	}
	if int64(len(encoded)) > maxResult {
		return MCPToolOutcome{}, mcpError(errs.ResourceExhausted, "MCP tool result exceeds its declared limit")
	}
	kind, outcome, payload, err := contract.DecodeContractOutcomeEnvelope(encoded)
	if err != nil {
		return MCPToolOutcome{}, ContractSystemError(fmt.Errorf("decode MCP tool outcome: %w", err))
	}
	result = MCPToolOutcome{Outcome: outcome}
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

func lookupMCPTool(assistantAddress, name string) (MCPToolRegistration, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	var match *MCPToolRegistration
	for _, registration := range global.mcpTools {
		if registration.Name != name || assistantAddress != "" && registration.AssistantAddress != assistantAddress {
			continue
		}
		if match != nil {
			return MCPToolRegistration{}, mcpError(errs.InvalidArgument, "MCP tool %s is ambiguous", name)
		}
		copy := registration
		match = &copy
	}
	if match == nil {
		return MCPToolRegistration{}, mcpError(errs.NotFound, "MCP tool not found")
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
	state.request.Headers.Set("traceparent", call.TraceContext["traceparent"])
	return state
}

func RegisterMCPTool(registration MCPToolRegistration) error {
	registration.ID = strings.TrimSpace(registration.ID)
	registration.Name = strings.TrimSpace(registration.Name)
	registration.AssistantAddress = strings.TrimSpace(registration.AssistantAddress)
	if registration.ID == "" || registration.Name == "" || registration.AssistantAddress == "" {
		return errors.New("runtime: MCP tool registration requires id, name, and assistant address")
	}
	if registration.DecodeInput == nil || registration.EncodeOutput == nil || registration.Invoke == nil {
		return fmt.Errorf("runtime: MCP tool %s has incomplete generated codecs or invoke path", registration.Name)
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

// mcpError is a typed MCP dispatch failure. The gateway reports its code to
// the client as the problem code, so callers never classify message text.
func mcpError(code errs.ErrCode, format string, args ...any) error {
	formatted := fmt.Errorf(format, args...)
	return &errs.Error{Code: code, Message: formatted.Error(), Cause: errors.Unwrap(formatted)}
}
