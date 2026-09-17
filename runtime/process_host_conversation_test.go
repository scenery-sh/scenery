package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"scenery.sh/internal/assistantruntime"
)

// assistantStreamHookForTest runs a hook while the gateway's event stream of a
// conversation is in flight, before the helper answers.
type assistantStreamHookForTest struct {
	*assistantruntime.FakeHelper
	hook func(assistantruntime.StreamRequest)
}

func (client assistantStreamHookForTest) StreamEvents(ctx context.Context, request assistantruntime.StreamRequest) (io.ReadCloser, error) {
	if client.hook != nil {
		client.hook(request)
	}
	return client.FakeHelper.StreamEvents(ctx, request)
}

// A host-local answer attests the generation it holds, and a tool call of its
// conversation made while it is in flight executes that generation; a tool call
// of any other conversation, or after the answer ends, executes the current one.
func TestProcessHostToolCallsOfAnAttestingConversationRunInItsGeneration(t *testing.T) {
	var hook func(assistantruntime.StreamRequest)
	testServer := newAssistantTestServerWithRegistration(t, assistantruntime.FakeConfig{}, func(registration *AssistantRegistration) {
		registration.Client = assistantStreamHookForTest{FakeHelper: registration.Client.(*assistantruntime.FakeHelper), hook: func(request assistantruntime.StreamRequest) {
			if hook != nil {
				hook(request)
			}
		}}
	})
	useLinkedProcessIdentityForTest(t)
	echoOne := startProcessHostTestBackend(t, "echo_echo", 601, "sha256:echo-1")
	echoTwo := startProcessHostTestBackend(t, "echo_echo", 602, "sha256:echo-2")
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "echo_echo", MCPTools: []ProcessHostMCPTool{
		{Process: "house_house", AssistantAddress: "support", Name: "house__describe"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	host.local, host.localRoutes = testServer.server.Handler, newRouteTable()
	for _, endpoint := range listEndpoints() {
		registerEndpointRoute(host.localRoutes, endpoint, func(http.ResponseWriter, *http.Request, routeParams) {})
	}
	control := serveProcessLinkForTest(t, http.HandlerFunc(host.serveControl))
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: control})
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "support#house/binding/describe_mcp", Name: "house__describe", AssistantAddress: "support",
		DecodeInput: func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) {
			return []byte(`{"kind":"result","name":"ok","value":` + string(value.([]byte)) + `}`), nil
		},
		Invoke: func(ctx context.Context, call MCPToolCallContext, input any) (any, error) {
			return InvokeContractBindingJSON(ctx, "echo/binding/echo_internal", "house", []byte(`{}`))
		},
	}); err != nil {
		t.Fatal(err)
	}
	var served atomic.Int32
	owner := serveProcessMCPOwnerForTest(t, &served)
	generation := func(number uint64, echo *processHostTestBackend) processGenerationManifest {
		return processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Identity: processHostTestBuild(number), Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
			Processes: map[string]processGenerationInstance{"house_house": owner, "echo_echo": echo.instance}}
	}
	if err := host.publish(generation(1, echoOne)); err != nil {
		t.Fatal(err)
	}
	setActiveProcessHost(host)
	t.Cleanup(func() { setActiveProcessHost(nil) })
	retire := func() int {
		status, _ := host.retire(1, false)
		return status
	}
	dispatch, _ := assistantMCPDispatchers()
	describe := func(principal, conversationDigest string) string {
		t.Helper()
		outcome, err := dispatch.CallTool(context.Background(), MCPToolCallContext{Principal: principal, AssistantAddress: "support", ConversationDigest: conversationDigest}, "house__describe", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("tool call: %v", err)
		}
		return string(outcome.Value)
	}

	created := assistantRequest(t, &http.Server{Handler: http.HandlerFunc(host.serveIngress)}, http.MethodPost, testServer.basePath, map[string]any{
		"message": map[string]string{"role": "user", "content": "hello"},
	}, nil)
	response := decodeCreateResponse(t, created)
	cookie := cookieFrom(t, created)
	var during []string
	var streamed assistantruntime.StreamRequest
	hook = func(request assistantruntime.StreamRequest) {
		streamed = request
		if err := host.publish(generation(2, echoTwo)); err != nil {
			t.Error(err)
		}
		during = append(during, describe(request.Principal, request.ConversationDigest), describe(request.Principal, "sha256:another-conversation"))
		if status := retire(); status != http.StatusConflict {
			t.Errorf("retirement under an attesting conversation = %d", status)
		}
	}
	events := assistantEventsRequest(t, &http.Server{Handler: http.HandlerFunc(host.serveIngress)}, response.EventsURL, cookie)
	hook = nil
	if events.Code != http.StatusOK || events.Header().Get(processGenerationHeader) != "1" {
		t.Fatalf("event stream = %d in generation %q", events.Code, events.Header().Get(processGenerationHeader))
	}
	assertProcessHostAttestation(t, events.Header(), processHostTestBuild(1))
	if len(during) != 2 || during[0] != `"echo_echo:sha256:echo-1"` || during[1] != `"echo_echo:sha256:echo-2"` {
		t.Fatalf("tool calls during the attesting stream = %v", during)
	}
	if got := describe(streamed.Principal, streamed.ConversationDigest); got != `"echo_echo:sha256:echo-2"` {
		t.Fatalf("tool call after the stream = %s", got)
	}
	if status := retire(); status != http.StatusNoContent {
		t.Fatalf("retirement after the stream = %d", status)
	}
}
