package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/runtimeapi"
)

func TestMCPToolDispatcherEstablishesAuthInvocationAndMetadata(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "house/binding/process_scene_mcp", Name: "process_scene", AssistantAddress: "app/assistant/support",
		CapabilityRevision: "sha256:contract", Limits: MCPToolLimits{MaxInputBytes: 256, MaxResultBytes: 256},
		DecodeInput: func(data []byte) (any, error) {
			var value map[string]any
			err := json.Unmarshal(data, &value)
			return value, err
		},
		EncodeOutput: func(value any) ([]byte, error) {
			return contract.MarshalContractOutcomeVariant("result", "processed", value, "json")
		},
		Invoke: func(ctx context.Context, call MCPToolCallContext, input any) (any, error) {
			auth := CurrentAuth()
			if auth == nil || auth.UID != "principal-1" {
				t.Fatalf("CurrentAuth = %#v", auth)
			}
			traceContext, _ := auth.Data.(map[string]any)["trace_context"].(map[string]string)
			if traceContext["trace_id"] != "trace-1" {
				t.Fatalf("trace context = %#v", traceContext)
			}
			invocation, ok := runtimeapi.InvocationFromContext(ctx)
			if !ok || invocation.Principal() != "principal-1" || invocation.ID() != "request-1" || invocation.TraceID() != "trace-1" {
				t.Fatalf("invocation = %#v, ok=%t", invocation, ok)
			}
			if call.IdempotencyKey != "idem-1" || call.ConversationDigest != "conv-1" {
				t.Fatalf("tool context = %#v", call)
			}
			return map[string]any{"ok": true, "input": input}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{
		Principal: "principal-1", AssistantAddress: "app/assistant/support", ConversationDigest: "conv-1",
		CapabilityRevision: "sha256:contract", RequestID: "request-1", TraceContext: map[string]string{"trace_id": "trace-1"}, IdempotencyKey: "idem-1",
	}, "process_scene", json.RawMessage(`{"value":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "processed" || !strings.Contains(string(result.Value), `"ok":true`) {
		t.Fatalf("MCP result = %#v", result)
	}
	if _, err := mcpcontract.MarshalOutcome(result); err != nil {
		t.Fatalf("MCP result envelope: %v", err)
	}
}

func TestMCPToolDispatcherEnforcesInputAndAssistantIsolation(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registration := MCPToolRegistration{
		ID: "binding/limited", Name: "limited", AssistantAddress: "assistant/a", Limits: MCPToolLimits{MaxInputBytes: 2, MaxResultBytes: 16},
		DecodeInput: func(data []byte) (any, error) { return data, nil }, EncodeOutput: func(value any) ([]byte, error) {
			return contract.MarshalContractOutcomeVariant("result", "ok", value, "json")
		},
		Invoke: func(context.Context, MCPToolCallContext, any) (any, error) { return map[string]bool{"ok": true}, nil },
	}
	if err := RegisterMCPTool(registration); err != nil {
		t.Fatal(err)
	}
	if _, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{Principal: "p", AssistantAddress: "assistant/a"}, "limited", json.RawMessage(`123`)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized input error = %v", err)
	}
	if _, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{Principal: "p", AssistantAddress: "assistant/b"}, "limited", json.RawMessage(`1`)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-assistant lookup error = %v", err)
	}
}

func TestMCPToolDispatcherMapsDeclaredErrorOutcome(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "binding/error", Name: "error", AssistantAddress: "assistant/a",
		DecodeInput: func(data []byte) (any, error) { return data, nil },
		EncodeOutput: func(value any) ([]byte, error) {
			return contract.MarshalContractOutcomeVariant("error", "invalid_input", value, "json")
		},
		Invoke: func(context.Context, MCPToolCallContext, any) (any, error) {
			return map[string]any{"code": "bad_input", "message": "scene is required"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{Principal: "principal-1", AssistantAddress: "assistant/a"}, "error", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "invalid_input" || len(result.Value) != 0 || string(result.Problem) != `{"code":"bad_input","message":"scene is required"}` {
		t.Fatalf("declared MCP error = %#v", result)
	}
	if _, err := mcpcontract.MarshalOutcome(result); err != nil {
		t.Fatalf("declared MCP error envelope: %v", err)
	}
}

func TestMCPToolDispatcherRunsContractPolicyPerCall(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	called := false
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "binding/policy", Name: "policy", AssistantAddress: "assistant/a",
		Policy: &ContractHTTPPolicy{
			BindingAddress: "binding/policy", AuthorizationStrategy: "deny_unless_allowed", AuthorizationRuleCount: 1,
			AuthorizationRules: []ContractAuthorizationRule{{Name: "allow", Effect: "allow", Expression: `principal.uid == "allowed"`}},
			PipelineSteps:      []string{},
		},
		DecodeInput: func(data []byte) (any, error) { return data, nil },
		EncodeOutput: func(value any) ([]byte, error) {
			return contract.MarshalContractOutcomeVariant("result", "ok", value, "json")
		},
		Invoke: func(context.Context, MCPToolCallContext, any) (any, error) {
			called = true
			return map[string]any{"authorized": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{Principal: "blocked", AssistantAddress: "assistant/a"}, "policy", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unauthorized MCP principal was accepted")
	}
	if called {
		t.Fatal("unauthorized MCP principal reached handler")
	}
	result, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{Principal: "allowed", AssistantAddress: "assistant/a"}, "policy", json.RawMessage(`{}`))
	if err != nil || result.Outcome != "ok" || !called {
		t.Fatalf("authorized MCP call result=%#v err=%v called=%t", result, err, called)
	}
}

func TestMCPRegistrationDoesNotExposePublicRoute(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "binding/private", Name: "private", AssistantAddress: "assistant/a",
		DecodeInput: func(data []byte) (any, error) { return data, nil },
		EncodeOutput: func(value any) ([]byte, error) {
			return contract.MarshalContractOutcomeVariant("result", "ok", value, "json")
		},
		Invoke: func(context.Context, MCPToolCallContext, any) (any, error) { return map[string]any{}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "http://public.example/mcp", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("public /mcp route status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPToolDispatcherDurableOutcomeIsReceiptOnly(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "binding/durable", Name: "durable", AssistantAddress: "assistant/a", Durable: true,
		DurableService: "house", DurableTask: "process_scene",
		DecodeInput:  func(data []byte) (any, error) { return data, nil },
		EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
		Invoke: func(context.Context, MCPToolCallContext, any) (any, error) {
			return runtimeapi.ExecutionReceipt{DurableIdentity: "house/process_scene", ExecutionID: "job-1", AcceptedRevision: "sha256:revision"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := (MCPToolDispatcher{}).CallTool(context.Background(), MCPToolCallContext{Principal: "principal-1", AssistantAddress: "assistant/a"}, "durable", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Value) != 0 || result.Receipt == nil || result.Outcome != "accepted" {
		t.Fatalf("durable MCP outcome = %#v", result)
	}
	if _, err := mcpcontract.MarshalOutcome(result); err != nil {
		t.Fatalf("durable MCP outcome envelope: %v", err)
	}
}

func TestMCPDurableOwnerCannotBeTakenOverByDedupeReplay(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	mcpDurableOwners.Store("house", "job-1", mcpDurableOwner{Principal: "first", TaskName: "process_scene"})
	mcpDurableOwners.Store("house", "job-1", mcpDurableOwner{Principal: "second", TaskName: "process_scene"})
	owner, ok := mcpDurableOwners.Load("house", "job-1")
	if !ok || owner.Principal != "first" {
		t.Fatalf("durable owner = %#v, ok=%t", owner, ok)
	}
}
