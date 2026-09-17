package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"scenery.sh/internal/assistantapi"
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

// A tool call of a conversation executes the generation of the run that makes
// it, and every event stream of the conversation attests that generation:
// overlapping streams agree, a stream whose attestation a newer run supersedes
// ends so its client resumes, a forced retirement makes the run's tool calls
// fail instead of executing a newer generation, and a replacement host keeps
// failing them until the conversation's next run.
func TestAssistantRunScopeSelectsTheGenerationOfItsToolCalls(t *testing.T) {
	var hook func(assistantruntime.StreamRequest)
	testServer := newAssistantTestServerWithRegistration(t, assistantruntime.FakeConfig{Text: "approval", CapabilityName: "delete", CapabilityInput: json.RawMessage(`{}`), RequireApproval: true}, func(registration *AssistantRegistration) {
		registration.Client = assistantStreamHookForTest{FakeHelper: registration.Client.(*assistantruntime.FakeHelper), hook: func(request assistantruntime.StreamRequest) {
			if current := hook; current != nil {
				hook = nil
				current(request)
			}
		}}
	})
	useLinkedProcessIdentityForTest(t)
	echo := []*processHostTestBackend{startProcessHostTestBackend(t, "echo_echo", 601, "sha256:echo-1"), startProcessHostTestBackend(t, "echo_echo", 602, "sha256:echo-2")}
	var serving atomic.Pointer[processHost]
	control := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { serving.Load().serveControl(w, req) }))
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
	state := t.TempDir()
	var host *processHost
	startHost := func() {
		t.Helper()
		started, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "echo_echo", MCPTools: []ProcessHostMCPTool{
			{Process: "house_house", AssistantAddress: "support", Name: "house__describe"},
		}}, processLinkTestToken, processHostTestContract)
		if err != nil {
			t.Fatal(err)
		}
		if err := started.openState(state); err != nil {
			t.Fatal(err)
		}
		started.local, started.localRoutes = testServer.server.Handler, newRouteTable()
		for _, endpoint := range listEndpoints() {
			registerEndpointRoute(started.localRoutes, endpoint, func(http.ResponseWriter, *http.Request, routeParams) {})
		}
		host = started
		serving.Store(host)
		setActiveProcessHost(host)
	}
	t.Cleanup(func() { setActiveProcessHost(nil) })
	publish := func(number uint64) {
		t.Helper()
		if err := host.publish(processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Identity: processHostTestBuild(number), Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
			Processes: map[string]processGenerationInstance{"house_house": owner, "echo_echo": echo[(number+1)%2].instance}}); err != nil {
			t.Fatal(err)
		}
	}
	ingress := func() *http.Server { return &http.Server{Handler: http.HandlerFunc(host.serveIngress)} }
	var principal, digest string
	describe := func() string {
		t.Helper()
		dispatch, _ := assistantMCPDispatchers()
		outcome, err := dispatch.CallTool(context.Background(), MCPToolCallContext{Principal: principal, AssistantAddress: "support", ConversationDigest: digest}, "house__describe", json.RawMessage(`{}`))
		if err != nil {
			return err.Error()
		}
		return string(outcome.Value)
	}
	startHost()
	publish(1)
	created := assistantRequest(t, ingress(), http.MethodPost, testServer.basePath, map[string]any{"message": map[string]string{"role": "user", "content": "hello"}}, nil)
	response := decodeCreateResponse(t, created)
	cookie := cookieFrom(t, created)
	events := func() *httptest.ResponseRecorder {
		t.Helper()
		return assistantEventsRequest(t, ingress(), response.EventsURL, cookie)
	}
	turn := func() {
		t.Helper()
		if recorder := assistantRequest(t, ingress(), http.MethodPost, strings.TrimSuffix(response.EventsURL, "/events")+"/turns", map[string]any{"message": map[string]string{"role": "user", "content": "again"}}, cookie); recorder.Code != http.StatusOK {
			t.Fatalf("turn = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	generation := func(recorder *httptest.ResponseRecorder) string {
		return recorder.Header().Get(processGenerationHeader)
	}

	// The run started in generation 1 and waits for approval. A stream opened
	// after generation 2 is published attests the run's generation, as does
	// the stream it overlaps, and the run's tool calls execute it.
	var inner *httptest.ResponseRecorder
	var during string
	hook = func(request assistantruntime.StreamRequest) {
		principal, digest = request.Principal, request.ConversationDigest
		publish(2)
		inner = events()
		during = describe()
	}
	outer := events()
	if generation(outer) != "1" || generation(inner) != "1" || during != `"echo_echo:sha256:echo-1"` {
		t.Fatalf("overlapping streams attest %q and %q while the run executes %s", generation(outer), generation(inner), during)
	}
	assertProcessHostAttestation(t, inner.Header(), processHostTestBuild(1))
	if status, _ := host.retire(1, false); status != http.StatusConflict {
		t.Fatalf("retiring the generation of a running assistant run = %d", status)
	}
	approval, ok := findEvent(decodePublicEvents(t, outer), assistantapi.EventApprovalRequired)
	if !ok {
		t.Fatal("the run waits for no approval")
	}
	var approvalID string
	_ = json.Unmarshal(eventDataMap(t, approval)["approval_id"], &approvalID)
	if recorder := assistantRequest(t, ingress(), http.MethodPost, strings.TrimSuffix(response.EventsURL, "/events")+"/approvals/"+approvalID, map[string]string{"decision": assistantapi.ApprovalApprove}, cookie); recorder.Code != http.StatusOK {
		t.Fatalf("approval = %d %s", recorder.Code, recorder.Body.String())
	}
	if got := describe(); got != `"echo_echo:sha256:echo-1"` {
		t.Fatalf("tool call of the approved run before its end was observed = %s", got)
	}
	// A stream observes the run's end, which releases its generation.
	if completed := events(); !strings.Contains(completed.Body.String(), assistantapi.EventRunCompleted) {
		t.Fatalf("stream after approval = %s", completed.Body.String())
	}
	if got := describe(); got != `"echo_echo:sha256:echo-2"` {
		t.Fatalf("tool call without a running run = %s", got)
	}
	if status, _ := host.retire(1, false); status != http.StatusNoContent {
		t.Fatalf("retiring generation 1 after its run ended = %d", status)
	}

	// A stream attesting generation 2 ends when a run starts in generation 3.
	hook = func(assistantruntime.StreamRequest) {
		publish(3)
		turn()
	}
	superseded := events()
	if superseded.Code != http.StatusOK || generation(superseded) != "2" || strings.TrimSpace(superseded.Body.String()) != "" {
		t.Fatalf("superseded stream = %d in %q: %s", superseded.Code, generation(superseded), superseded.Body.String())
	}
	if resumed := events(); generation(resumed) != "3" || describe() != `"echo_echo:sha256:echo-1"` {
		t.Fatalf("resumed stream attests %q while the run executes %s", generation(resumed), describe())
	}

	// A forced retirement of the run's generation ends the streams attesting it
	// and fails the run's tool calls instead of executing generation 4.
	hook = func(assistantruntime.StreamRequest) {
		publish(4)
		if status, _ := host.retire(3, true); status != http.StatusNoContent {
			t.Errorf("forced retirement = %d", status)
		}
	}
	if retired := events(); generation(retired) != "3" || strings.TrimSpace(retired.Body.String()) != "" {
		t.Fatalf("stream of a retired generation = %q: %s", generation(retired), retired.Body.String())
	}
	if got := describe(); !strings.Contains(got, "generation 3, which is no longer dispatchable") {
		t.Fatalf("tool call of a run whose generation was retired = %s", got)
	}
	if current := events(); generation(current) != "4" {
		t.Fatalf("stream of a conversation whose run cannot execute attests %q", generation(current))
	}

	// A replacement host replays the run scope and keeps failing its tool calls
	// until the conversation's next run.
	startHost()
	publish(6)
	if got := describe(); !strings.Contains(got, "generation 3, which is no longer dispatchable") {
		t.Fatalf("tool call of a run from the previous host = %s", got)
	}
	turn()
	if got := describe(); got != `"echo_echo:sha256:echo-2"` {
		t.Fatalf("tool call of the next run on the replacement host = %s", got)
	}
}
