package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
)

const processHostTestContract = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// processHostTestBackend emulates one service process instance: it answers
// forwarded HTTP and dispatched calls with its own identity headers.
type processHostTestBackend struct {
	name     string
	target   processLinkTarget
	instance processGenerationInstance
	release  chan struct{}
	mu       sync.Mutex
	seen     []map[string]string
}

func startProcessHostTestBackend(t *testing.T, name string, pid int, revision string) *processHostTestBackend {
	t.Helper()
	backend := &processHostTestBackend{name: name}
	backend.target = serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		identity := backend.instance.Identity
		w.Header().Set(processIdentityContractHdr, identity.ContractRevision)
		w.Header().Set(processIdentityImplHeader, identity.ImplementationRevision)
		w.Header().Set(processIdentityBuildHeader, identity.BuildInputDigest)
		w.Header().Set(processIdentityTargetHeader, identity.GoTarget)
		w.Header().Set(processIdentityPIDHeader, strconv.Itoa(backend.instance.PID))
		if req.URL.Path == "/wrong-identity" {
			w.Header().Set(processIdentityImplHeader, "sha256:other")
		}
		if req.URL.Path == "/slow" {
			<-backend.release
		}
		seen := map[string]string{"method": req.Method, "uri": req.RequestURI, "host": req.Host, "generation": req.Header.Get(processGenerationHeader),
			"forwarded_for": req.Header.Get("X-Forwarded-For"), "binding": req.Header.Get(processLinkBindingHeader)}
		backend.mu.Lock()
		backend.seen = append(backend.seen, seen)
		backend.mu.Unlock()
		if req.URL.Path == processLinkBindingPath {
			writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: json.RawMessage(strconv.Quote(name + ":" + revision))})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"process": name, "revision": revision})
	}))
	backend.instance = processGenerationInstance{Network: backend.target.Network, Address: backend.target.Address, PID: pid, Identity: processInstanceIdentity{
		ContractRevision: processHostTestContract, ImplementationRevision: revision, BuildInputDigest: revision + "-inputs", GoTarget: "development",
	}}
	backend.release = make(chan struct{})
	return backend
}

func (b *processHostTestBackend) last() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen[len(b.seen)-1]
}

func processHostTestRequest(t *testing.T, handler http.HandlerFunc, method, target string, headers map[string]string) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader([]byte(`{}`)))
	request.Host = "api.example.test"
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	var body map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	return recorder, body
}

func TestProcessHostForwardsEachRequestToTheOwningProcess(t *testing.T) {
	echo := startProcessHostTestBackend(t, "echo_echo", 101, "sha256:echo-1")
	greeter := startProcessHostTestBackend(t, "greeter_greeter", 102, "sha256:greeter-1")
	host, err := newProcessHost(ProcessHostConfig{Name: "multiservice", Fallback: "echo_echo", Routes: []ProcessHostRoute{
		{Process: "echo_echo", Methods: []string{"POST"}, Path: "/echo"},
		{Process: "echo_echo", Methods: []string{"GET"}, Path: "/items/:id"},
		{Process: "echo_echo", Methods: []string{"GET"}, Path: "/wrong-identity"},
		{Process: "greeter_greeter", Methods: []string{"POST"}, Path: "/greet"},
		{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/items/special"},
		{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/files/*path", PathTail: true},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	if recorder, _ := processHostTestRequest(t, host.serveIngress, "POST", "/echo", nil); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("ingress before publication = %d", recorder.Code)
	}
	if err := host.publish(processGenerationManifest{Generation: 1, ContractRevision: processHostTestContract, Processes: map[string]processGenerationInstance{
		"echo_echo": echo.instance, "greeter_greeter": greeter.instance,
	}}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		method, target string
		backend        *processHostTestBackend
		headers        map[string]string
	}{
		{method: "POST", target: "/greet?lang=cs", backend: greeter},
		{method: "POST", target: "/echo", backend: echo},
		{method: "GET", target: "/items/special", backend: greeter},
		{method: "GET", target: "/items/42", backend: echo},
		{method: "GET", target: "/files/a/b%2Fc", backend: greeter},
		{method: "DELETE", target: "/greet", backend: greeter},
		{method: "OPTIONS", target: "/items/special", backend: greeter, headers: map[string]string{"Access-Control-Request-Method": "PUT"}},
		{method: "OPTIONS", target: "/items/42", backend: echo, headers: map[string]string{"Access-Control-Request-Method": "GET"}},
		{method: "GET", target: "/__scenery/config", backend: echo},
		{method: "GET", target: "/unknown", backend: echo, headers: map[string]string{processGenerationHeader: "77", "X-Forwarded-For": "203.0.113.7"}},
	} {
		recorder, body := processHostTestRequest(t, host.serveIngress, check.method, check.target, check.headers)
		seen := check.backend.last()
		if recorder.Code != http.StatusOK || body["process"] != check.backend.name || seen["method"] != check.method || seen["uri"] != check.target || seen["generation"] != "1" || seen["host"] != "api.example.test" {
			t.Errorf("%s %s reached %#v (status %d, seen %#v), want %s in generation 1", check.method, check.target, body, recorder.Code, seen, check.backend.name)
		}
		// An answer names the generation that served it.
		if recorder.Header().Get(processGenerationHeader) != "1" {
			t.Errorf("%s %s answered without its generation: %q", check.method, check.target, recorder.Header().Get(processGenerationHeader))
		}
	}
	if seen := echo.last(); seen["forwarded_for"] != "203.0.113.7" {
		t.Fatalf("forwarded headers = %#v", seen)
	}
	recorder, _ := processHostTestRequest(t, host.serveIngress, "GET", "/wrong-identity", nil)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "echo_echo") {
		t.Fatalf("answer from an unpublished identity = %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := newProcessHost(ProcessHostConfig{Fallback: "echo_echo", Routes: []ProcessHostRoute{{Process: "echo_echo", Path: "/x"}}}, processLinkTestToken, processHostTestContract); err == nil {
		t.Fatal("route without methods was accepted")
	}
}

func TestProcessHostPinsRequestsAndCallsToTheirGeneration(t *testing.T) {
	echoOne := startProcessHostTestBackend(t, "echo_echo", 201, "sha256:echo-1")
	echoTwo := startProcessHostTestBackend(t, "echo_echo", 202, "sha256:echo-2")
	greeter := startProcessHostTestBackend(t, "greeter_greeter", 203, "sha256:greeter-1")
	host, err := newProcessHost(ProcessHostConfig{Name: "multiservice", Fallback: "echo_echo", Routes: []ProcessHostRoute{
		{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/slow"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	bindings := map[string]string{"echo/binding/echo_internal": "echo_echo"}
	generation := func(number uint64, echo *processHostTestBackend) processGenerationManifest {
		return processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Bindings: bindings, Processes: map[string]processGenerationInstance{
			"echo_echo": echo.instance, "greeter_greeter": greeter.instance,
		}}
	}
	control := serveProcessLinkForTest(t, http.HandlerFunc(host.serveControl))
	publish := func(manifest processGenerationManifest) int {
		body, _ := json.Marshal(manifest)
		request, _ := http.NewRequest(http.MethodPut, "http://host"+processGenerationsPath, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+processLinkTestToken)
		response, err := processLinkClient(control).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if status := publish(generation(1, echoOne)); status != http.StatusNoContent {
		t.Fatalf("publish generation 1 = %d", status)
	}
	pinned := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder, _ := processHostTestRequest(t, host.serveIngress, "GET", "/slow", nil)
		pinned <- recorder
	}()
	deadline := time.Now().Add(time.Second)
	for host.status().Generations[0].InFlight == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for name, stale := range map[string]processGenerationManifest{
		"repeated": generation(1, echoTwo),
		"wrong-contract": func() processGenerationManifest {
			m := generation(2, echoTwo)
			m.ContractRevision = "sha256:other"
			return m
		}(),
		"unknown-owner": func() processGenerationManifest {
			m := generation(2, echoTwo)
			m.Bindings = map[string]string{"x/binding/y": "missing"}
			return m
		}(),
		"missing-route": func() processGenerationManifest {
			m := generation(2, echoTwo)
			delete(m.Processes, "greeter_greeter")
			return m
		}(),
	} {
		if status := publish(stale); status != http.StatusConflict {
			t.Errorf("%s generation publication = %d", name, status)
		}
	}
	if status := publish(generation(2, echoTwo)); status != http.StatusNoContent {
		t.Fatalf("publish generation 2 = %d", status)
	}
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: control}
	call := func(pinnedGeneration uint64) (string, error) {
		state := &requestState{processGeneration: pinnedGeneration}
		ctx := withState(runtimeapi.WithInvocation(context.Background(), runtimeapi.NewInvocation("invocation-9", "", "", "", time.Time{})), state)
		restore := enterState(state)
		defer restore()
		invocation, _ := runtimeapi.InvocationFromContext(ctx)
		output, err := invokeProcessLinkedBindingJSON(ctx, config, "echo/binding/echo_internal", "greeter", invocation, []byte(`{}`))
		var value string
		_ = json.Unmarshal(output, &value)
		return value, err
	}
	if value, err := call(1); err != nil || value != "echo_echo:sha256:echo-1" || echoOne.last()["binding"] != "echo/binding/echo_internal" {
		t.Fatalf("call pinned to generation 1 = %q, %v", value, err)
	}
	if value, err := call(0); err != nil || value != "echo_echo:sha256:echo-2" {
		t.Fatalf("unpinned call = %q, %v", value, err)
	}
	retire := func(number uint64) int {
		request, _ := http.NewRequest(http.MethodDelete, "http://host"+processGenerationsPath+"/"+strconv.FormatUint(number, 10), nil)
		request.Header.Set("Authorization", "Bearer "+processLinkTestToken)
		response, err := processLinkClient(control).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if status := retire(1); status != http.StatusConflict {
		t.Fatalf("retire generation with an in-flight request = %d", status)
	}
	if status := retire(2); status != http.StatusConflict {
		t.Fatalf("retire current generation = %d", status)
	}
	close(greeter.release)
	if recorder := <-pinned; recorder.Code != http.StatusOK || greeter.last()["generation"] != "1" {
		t.Fatalf("in-flight request = %d, seen %#v", recorder.Code, greeter.last())
	}
	if status := retire(1); status != http.StatusNoContent {
		t.Fatalf("retire drained generation = %d", status)
	}
	if _, err := call(1); !isUnavailableDelivery(err, "not_sent") {
		t.Fatalf("call pinned to a retired generation = %#v", err)
	}
	status := host.status()
	if status.Current != 2 || len(status.Generations) != 1 || status.Generations[0].Processes["echo_echo"] != 202 {
		t.Fatalf("host status = %#v", status)
	}
	// The status names the exact implementation every instance of a retained
	// generation runs, so a check can bind its evidence to those identities.
	if identity := status.Generations[0].Instances["echo_echo"]; identity != echoTwo.instance.Identity || identity.ImplementationRevision != "sha256:echo-2" {
		t.Fatalf("published instance identity = %#v", identity)
	}
}

func isUnavailableDelivery(err error, delivery string) bool {
	typed, ok := errs.As(err)
	return ok && typed.Code == errs.Unavailable && typed.Meta["delivery"] == delivery
}

// useLinkedProcessIdentityForTest links this test process with the identity a
// published service instance in processHostTestContract generations reports.
func useLinkedProcessIdentityForTest(t *testing.T) {
	t.Helper()
	for pointer, value := range map[*string]string{
		&linkedContractRevision: processHostTestContract, &linkedImplementationRevision: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		&linkedBuildInputDigest: "sha256:3333333333333333333333333333333333333333333333333333333333333333", &linkedGoTarget: "development",
	} {
		previous := *pointer
		*pointer = value
		t.Cleanup(func() { *pointer = previous })
	}
}

// serveProcessMCPOwnerForTest serves the service-process MCP endpoints of this
// test process on a private socket and counts the requests it answers.
func serveProcessMCPOwnerForTest(t *testing.T, served *atomic.Int32) processGenerationInstance {
	t.Helper()
	owner := &server{}
	socket := serveProcessLinkForTest(t, withProcessGeneration(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		served.Add(1)
		switch req.URL.Path {
		case processMCPCallPath:
			owner.handleProcessMCPCall(w, req, nil)
		case processMCPDurablePath:
			owner.handleProcessMCPDurable(w, req, nil)
		default:
			http.NotFound(w, req)
		}
	})))
	return processGenerationInstance{Network: socket.Network, Address: socket.Address, PID: os.Getpid(), Identity: processInstanceIdentity(CurrentLinkedContractBundle())}
}

func TestProcessHostForwardsMCPToolsAndAuthorizesDurableReceiptsAcrossReplacement(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useLinkedProcessIdentityForTest(t)
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: processLinkTarget{Network: "unix", Address: "/unused"}})
	calls := 0
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "app/assistant/support#house/binding/process_scene_mcp", Name: "house__process_scene", AssistantAddress: "app/assistant/support",
		DecodeInput:  func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
		Durable:      true, DurableService: "house", DurableTask: "process_scene",
		Invoke: func(ctx context.Context, call MCPToolCallContext, input any) (any, error) {
			calls++
			if auth := CurrentAuth(); auth == nil || auth.UID != "principal-1" || input != `{"scene":"a"}` {
				t.Errorf("owner call auth %#v input %#v", auth, input)
			}
			return runtimeapi.ExecutionReceipt{DurableIdentity: "house/process_scene", ExecutionID: "execution-1", AcceptedRevision: processHostTestContract}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	var acceptedBy, replacementServed atomic.Int32
	accepting := serveProcessMCPOwnerForTest(t, &acceptedBy)
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "house_house", MCPTools: []ProcessHostMCPTool{
		{Process: "house_house", AssistantAddress: "app/assistant/support", Name: "house__process_scene"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.publish(processGenerationManifest{Generation: 1, ContractRevision: processHostTestContract, Processes: map[string]processGenerationInstance{"house_house": accepting}}); err != nil {
		t.Fatal(err)
	}
	setActiveProcessHost(host)
	t.Cleanup(func() { setActiveProcessHost(nil) })
	dispatch, durable := assistantMCPDispatchers()
	call := MCPToolCallContext{Principal: "principal-1", AssistantAddress: "app/assistant/support", RequestID: "request-1"}
	outcome, err := dispatch.CallTool(context.Background(), call, "house__process_scene", json.RawMessage(`{"scene":"a"}`))
	if err != nil || outcome.Outcome != "accepted" || outcome.Receipt == nil || outcome.Receipt.ExecutionID != "execution-1" || calls != 1 {
		t.Fatalf("forwarded MCP call = %#v, %v (calls %d)", outcome, err, calls)
	}
	if _, err := dispatch.CallTool(context.Background(), call, "house__missing", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("unknown MCP tool = %v", err)
	}
	// A replacement instance starts without the accepting process's receipt
	// records; the host authorizes the principal and names the durable task.
	replacement := serveProcessMCPOwnerForTest(t, &replacementServed)
	if err := host.publish(processGenerationManifest{Generation: 2, ContractRevision: processHostTestContract, Processes: map[string]processGenerationInstance{"house_house": replacement}}); err != nil {
		t.Fatal(err)
	}
	mcpDurableOwners.Lock()
	mcpDurableOwners.values, mcpDurableOwners.order = map[string]mcpDurableOwner{}, nil
	mcpDurableOwners.Unlock()
	other := call
	other.Principal = "principal-2"
	if _, err := durable.Status(context.Background(), other, "execution-1"); err == nil || !strings.Contains(err.Error(), "not_found") || replacementServed.Load() != 0 {
		t.Fatalf("status for another principal = %v (replacement served %d)", err, replacementServed.Load())
	}
	// Without a durable store the replacement reports its own store failure,
	// which proves it read the authorized receipt instead of a missing record.
	for _, operation := range []func(context.Context, MCPToolCallContext, string) (json.RawMessage, error){durable.Status, durable.Cancel} {
		if _, err := operation(context.Background(), call, "execution-1"); err == nil || !strings.Contains(err.Error(), "durable execution store is unavailable") {
			t.Fatalf("durable operation after replacement = %v", err)
		}
	}
	if replacementServed.Load() != 2 || acceptedBy.Load() != 1 {
		t.Fatalf("replacement served %d durable requests, accepting instance served %d requests", replacementServed.Load(), acceptedBy.Load())
	}
}

func TestProcessHostPinsForwardedMCPToolCallsAndTheirInternalCalls(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useLinkedProcessIdentityForTest(t)
	echoOne := startProcessHostTestBackend(t, "echo_echo", 301, "sha256:echo-1")
	echoTwo := startProcessHostTestBackend(t, "echo_echo", 302, "sha256:echo-2")
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "echo_echo", MCPTools: []ProcessHostMCPTool{
		{Process: "house_house", AssistantAddress: "app/assistant/support", Name: "house__describe"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: serveProcessLinkForTest(t, http.HandlerFunc(host.serveControl))})
	started, resume := make(chan struct{}), make(chan struct{})
	var invocations atomic.Int32
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "app/assistant/support#house/binding/describe_mcp", Name: "house__describe", AssistantAddress: "app/assistant/support",
		DecodeInput: func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) {
			return []byte(`{"kind":"result","name":"ok","value":` + string(value.([]byte)) + `}`), nil
		},
		Invoke: func(ctx context.Context, call MCPToolCallContext, input any) (any, error) {
			if invocations.Add(1) == 1 {
				close(started)
				<-resume
			}
			return InvokeContractBindingJSON(ctx, "echo/binding/echo_internal", "house", []byte(`{}`))
		},
	}); err != nil {
		t.Fatal(err)
	}
	var served atomic.Int32
	owner := serveProcessMCPOwnerForTest(t, &served)
	generation := func(number uint64, echo *processHostTestBackend) processGenerationManifest {
		return processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
			Processes: map[string]processGenerationInstance{"house_house": owner, "echo_echo": echo.instance}}
	}
	if err := host.publish(generation(1, echoOne)); err != nil {
		t.Fatal(err)
	}
	setActiveProcessHost(host)
	t.Cleanup(func() { setActiveProcessHost(nil) })
	dispatch, _ := assistantMCPDispatchers()
	call := MCPToolCallContext{Principal: "principal-1", AssistantAddress: "app/assistant/support", RequestID: "request-1"}
	type result struct {
		outcome MCPToolOutcome
		err     error
	}
	pinned := make(chan result, 1)
	go func() {
		outcome, err := dispatch.CallTool(context.Background(), call, "house__describe", json.RawMessage(`{}`))
		pinned <- result{outcome, err}
	}()
	<-started
	if err := host.publish(generation(2, echoTwo)); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if got := <-pinned; got.err != nil || string(got.outcome.Value) != `"echo_echo:sha256:echo-1"` {
		t.Fatalf("tool call started in generation 1 = %s, %v", got.outcome.Value, got.err)
	}
	if outcome, err := dispatch.CallTool(context.Background(), call, "house__describe", json.RawMessage(`{}`)); err != nil || string(outcome.Value) != `"echo_echo:sha256:echo-2"` {
		t.Fatalf("tool call started in generation 2 = %s, %v", outcome.Value, err)
	}
}

func TestProcessHostForcedRetirementEndsDispatchWithinTheGeneration(t *testing.T) {
	echo := startProcessHostTestBackend(t, "echo_echo", 401, "sha256:echo-1")
	greeter := startProcessHostTestBackend(t, "greeter_greeter", 402, "sha256:greeter-1")
	host, err := newProcessHost(ProcessHostConfig{Name: "multiservice", Fallback: "echo_echo", Routes: []ProcessHostRoute{
		{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/slow"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	manifest := func(number uint64) processGenerationManifest {
		return processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
			Processes: map[string]processGenerationInstance{"echo_echo": echo.instance, "greeter_greeter": greeter.instance}}
	}
	if err := host.publish(manifest(1)); err != nil {
		t.Fatal(err)
	}
	pinned := make(chan int, 1)
	go func() {
		recorder, _ := processHostTestRequest(t, host.serveIngress, "GET", "/slow", nil)
		pinned <- recorder.Code
	}()
	deadline := time.Now().Add(time.Second)
	for host.status().Generations[0].InFlight == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := host.publish(manifest(2)); err != nil {
		t.Fatal(err)
	}
	control := serveProcessLinkForTest(t, http.HandlerFunc(host.serveControl))
	retire := func(query string) int {
		request, _ := http.NewRequest(http.MethodDelete, "http://host"+processGenerationsPath+"/1"+query, nil)
		request.Header.Set("Authorization", "Bearer "+processLinkTestToken)
		response, err := processLinkClient(control).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if status := retire("?force=yes"); status != http.StatusBadRequest {
		t.Fatalf("malformed forced retirement = %d", status)
	}
	if status := retire(""); status != http.StatusConflict {
		t.Fatalf("retirement with work in flight = %d", status)
	}
	if status := retire("?force=true"); status != http.StatusNoContent {
		t.Fatalf("forced retirement = %d", status)
	}
	if generation := host.acquire(1); generation != nil {
		t.Fatal("a force-retired generation stayed dispatchable")
	}
	close(greeter.release)
	if code := <-pinned; code != http.StatusOK {
		t.Fatalf("request forwarded before forced retirement = %d", code)
	}
	if status := host.status(); status.Current != 2 || len(status.Generations) != 1 {
		t.Fatalf("host status after forced retirement = %#v", status)
	}
}

// Streams and upgraded connections stay pinned work until they end, so their
// generation is not retired and their instances are not stopped under them.
func TestProcessHostCountsStreamsAndUpgradedConnectionsAsPinnedWork(t *testing.T) {
	release := make(chan struct{})
	instance := processGenerationInstance{PID: 501, Identity: processInstanceIdentity{
		ContractRevision: processHostTestContract, ImplementationRevision: "sha256:stream-1", BuildInputDigest: "sha256:stream-inputs", GoTarget: "development",
	}}
	target := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for header, value := range map[string]string{processIdentityContractHdr: instance.Identity.ContractRevision, processIdentityImplHeader: instance.Identity.ImplementationRevision,
			processIdentityBuildHeader: instance.Identity.BuildInputDigest, processIdentityTargetHeader: instance.Identity.GoTarget, processIdentityPIDHeader: strconv.Itoa(instance.PID)} {
			w.Header().Set(header, value)
		}
		if req.URL.Path == "/upgrade" {
			w.Header().Set("Connection", "Upgrade")
			w.Header().Set("Upgrade", "probe")
			w.WriteHeader(http.StatusSwitchingProtocols)
			conn, buffered, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			<-release
			_, _ = buffered.WriteString("closing")
			_ = buffered.Flush()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: first\n\n"))
		_ = http.NewResponseController(w).Flush()
		<-release
		_, _ = w.Write([]byte("data: last\n\n"))
	}))
	instance.Network, instance.Address = target.Network, target.Address
	host, err := newProcessHost(ProcessHostConfig{Name: "streams", Fallback: "stream_stream"}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	manifest := func(number uint64) processGenerationManifest {
		return processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Processes: map[string]processGenerationInstance{"stream_stream": instance}}
	}
	if err := host.publish(manifest(1)); err != nil {
		t.Fatal(err)
	}
	public := serveProcessLinkForTest(t, http.HandlerFunc(host.serveIngress))
	client := processLinkClient(public)
	stream, err := client.Get("http://host/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Body.Close() }()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(stream.Body, first); err != nil || string(first) != "data: first\n\n" {
		t.Fatalf("first stream event = %q, %v", first, err)
	}
	request, _ := http.NewRequest(http.MethodGet, "http://host/upgrade", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "probe")
	upgraded, err := client.Do(request)
	if err != nil || upgraded.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade = %v, %v", upgraded, err)
	}
	upgradedConn, ok := upgraded.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatalf("upgraded body %T is not a connection", upgraded.Body)
	}
	defer func() { _ = upgradedConn.Close() }()
	if err := host.publish(manifest(2)); err != nil {
		t.Fatal(err)
	}
	if status, _ := host.retire(1, false); status != http.StatusConflict || host.status().Generations[0].InFlight != 2 {
		t.Fatalf("retiring a generation with an open stream and upgraded connection = %d (%#v)", status, host.status())
	}
	close(release)
	if rest, err := io.ReadAll(stream.Body); err != nil || string(rest) != "data: last\n\n" {
		t.Fatalf("stream end = %q, %v", rest, err)
	}
	if rest, err := io.ReadAll(upgradedConn); err != nil || string(rest) != "closing" {
		t.Fatalf("upgraded connection end = %q, %v", rest, err)
	}
	// The host copies both directions of an upgraded connection until both end.
	_ = upgradedConn.Close()
	deadline := time.Now().Add(time.Second)
	for host.status().Generations[0].InFlight != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status, _ := host.retire(1, false); status != http.StatusNoContent {
		t.Fatalf("retiring the generation after its stream and connection ended = %d", status)
	}
}

func TestProcessDurableReceiptOwnersAreBoundedAndFailClosed(t *testing.T) {
	var owners processHostDurableOwners
	owner := processHostDurableOwner{process: "house_house", service: "house", taskName: "process_scene"}
	for index := range processHostDurableOwnerLimit + 1 {
		owners.store("principal-1", strconv.Itoa(index), owner)
	}
	if _, ok := owners.load("principal-1", "0"); ok {
		t.Fatal("the oldest receipt beyond the limit stayed authorized")
	}
	if got, ok := owners.load("principal-1", strconv.Itoa(processHostDurableOwnerLimit)); !ok || got != owner {
		t.Fatalf("newest receipt = %#v, %v", got, ok)
	}
	conflicting := owner
	conflicting.process = "maps_maps"
	owners.store("principal-1", "1", conflicting)
	if _, ok := owners.load("principal-1", "1"); ok {
		t.Fatal("an execution ID accepted by two owners stayed authorized")
	}
	var local mcpDurableOwnerStore
	for index := range mcpDurableOwnerLimit + 1 {
		local.Store("house", strconv.Itoa(index), mcpDurableOwner{Principal: "principal-1", TaskName: "process_scene"})
	}
	if _, ok := local.Load("house", "0"); ok || len(local.values) != mcpDurableOwnerLimit {
		t.Fatalf("process-local receipts = %d, oldest retained %v", len(local.values), ok)
	}
}

// processBackgroundForTest records activation and drain requests.
type processBackgroundForTest struct {
	activations, drains int
	drainErr            error
}

func (background *processBackgroundForTest) activate() error {
	background.activations++
	return nil
}

func (background *processBackgroundForTest) drain(context.Context) error {
	background.drains++
	return background.drainErr
}

func TestProcessBackgroundActivationAndDrainRequireTheSessionToken(t *testing.T) {
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: processLinkTarget{Network: "unix", Address: "/unused"}})
	background := &processBackgroundForTest{}
	setProcessBackground(background)
	t.Cleanup(func() { setProcessBackground(nil) })
	owner := &server{}
	request := func(handler func(http.ResponseWriter, *http.Request, routeParams), path, token string) int {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler(recorder, request, nil)
		return recorder.Code
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request, routeParams){owner.handleProcessActivate, owner.handleProcessDrain} {
		if status := request(handler, processDrainPath, "wrong-token-wrong-token-wrong-token"); status != http.StatusUnauthorized {
			t.Fatalf("background control with a wrong token = %d", status)
		}
	}
	if status := request(owner.handleProcessActivate, processActivatePath, processLinkTestToken); status != http.StatusNoContent || background.activations != 1 {
		t.Fatalf("activation = %d (%d activations)", status, background.activations)
	}
	if status := request(owner.handleProcessDrain, processDrainPath, processLinkTestToken); status != http.StatusNoContent || background.drains != 1 {
		t.Fatalf("drain = %d (%d drains)", status, background.drains)
	}
	background.drainErr = context.DeadlineExceeded
	if status := request(owner.handleProcessDrain, processDrainPath, processLinkTestToken); status != http.StatusAccepted {
		t.Fatalf("drain whose running work outlived the wait = %d", status)
	}
}

func TestRuntimeBackgroundStartsOnlyOnActivationAndNeverAfterDrain(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	starts, stops := 0, 0
	newBackground := func() *runtimeBackground {
		durable := &durableRuntime{ctx: context.Background(), start: func(context.Context) func(context.Context) error {
			starts++
			return func(context.Context) error { stops++; return nil }
		}}
		return &runtimeBackground{ctx: context.Background(), durable: durable}
	}
	background := newBackground()
	if starts != 0 {
		t.Fatal("background work started before activation")
	}
	for range 2 {
		if err := background.activate(); err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 {
		t.Fatalf("durable background started %d times", starts)
	}
	if err := background.drain(context.Background()); err != nil || stops != 1 {
		t.Fatalf("drain = %v (%d stops)", err, stops)
	}
	if err := background.activate(); err == nil || !strings.Contains(err.Error(), "failed_precondition") || starts != 1 {
		t.Fatalf("activation after drain = %v (%d starts)", err, starts)
	}
	revoked := newBackground()
	if err := revoked.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := revoked.activate(); err == nil || starts != 1 {
		t.Fatalf("activation of a runtime drained before activation = %v (%d starts)", err, starts)
	}
	if err := shutdownRuntime(nil, newBackground()); err != nil || starts != 1 {
		t.Fatalf("shutdown of an inactive runtime = %v (%d starts)", err, starts)
	}
}

func TestProcessHostServesItsOwnApplicationEndpointsBeforePublication(t *testing.T) {
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "house_house"}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	host.local = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("local:" + req.URL.Path)) })
	host.localRoutes = newRouteTable()
	host.localRoutes.Handle([]string{http.MethodPost}, "/assistants/support/:conversation_id/turns", func(http.ResponseWriter, *http.Request, routeParams) {})
	recorder, _ := processHostTestRequest(t, host.serveIngress, "POST", "/assistants/support/c1/turns", nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "local:/assistants/support/c1/turns" {
		t.Fatalf("host application endpoint = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder, _ := processHostTestRequest(t, host.serveIngress, "POST", "/greet", nil); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("service route before publication = %d", recorder.Code)
	}
}

// A disposable session can fail selected work on purpose: each rule applies a
// bounded number of times and names the delivery its caller must assume.
func TestProcessHostFailsSelectedWorkOnPurpose(t *testing.T) {
	echo := startProcessHostTestBackend(t, "echo_echo", 601, "sha256:echo-1")
	greeter := startProcessHostTestBackend(t, "greeter_greeter", 602, "sha256:greeter-1")
	host, err := newProcessHost(ProcessHostConfig{Name: "multiservice", Fallback: "echo_echo", Routes: []ProcessHostRoute{
		{Process: "greeter_greeter", Methods: []string{"POST"}, Path: "/greet"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.publish(processGenerationManifest{Generation: 1, ContractRevision: processHostTestContract,
		Bindings:  map[string]string{"echo/binding/echo_internal": "echo_echo"},
		Processes: map[string]processGenerationInstance{"echo_echo": echo.instance, "greeter_greeter": greeter.instance}}); err != nil {
		t.Fatal(err)
	}
	control := serveProcessLinkForTest(t, http.HandlerFunc(host.serveControl))
	faults := func(body string) int {
		request, _ := http.NewRequest(http.MethodPut, "http://host"+processFaultsPath, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+processLinkTestToken)
		response, err := processLinkClient(control).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if status := faults(`{"faults":[{"mode":"linger"}]}`); status != http.StatusBadRequest {
		t.Fatalf("unsupported fault mode = %d", status)
	}
	if status := faults(`{"faults":[{"binding":"echo/binding/echo_internal","mode":"refuse","message":"echo refused"},{"path":"/greet","mode":"abort"}]}`); status != http.StatusNoContent {
		t.Fatalf("publish fault rules = %d", status)
	}
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: control}
	call := func() (string, error) {
		state := &requestState{processGeneration: 1}
		ctx := withState(runtimeapi.WithInvocation(context.Background(), runtimeapi.NewInvocation("invocation-7", "", "", "", time.Time{})), state)
		restore := enterState(state)
		defer restore()
		invocation, _ := runtimeapi.InvocationFromContext(ctx)
		output, err := invokeProcessLinkedBindingJSON(ctx, config, "echo/binding/echo_internal", "greeter", invocation, []byte(`{}`))
		var value string
		_ = json.Unmarshal(output, &value)
		return value, err
	}
	if _, err := call(); !isUnavailableDelivery(err, "not_sent") || !strings.Contains(err.Error(), "echo refused") {
		t.Fatalf("refused internal call = %v", err)
	}
	// The rule applied once; the next call reaches its owner again.
	if value, err := call(); err != nil || value != "echo_echo:sha256:echo-1" {
		t.Fatalf("call after the rule was consumed = %q, %v", value, err)
	}
	recorder, _ := processHostTestRequest(t, host.serveIngress, "POST", "/greet", nil)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("aborted request = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder, body := processHostTestRequest(t, host.serveIngress, "POST", "/greet", nil); recorder.Code != http.StatusOK || body["process"] != "greeter_greeter" {
		t.Fatalf("request after the rule was consumed = %d %#v", recorder.Code, body)
	}
	// A delay outlives its caller, which then assumes an unknown delivery.
	if status := faults(`{"faults":[{"process":"echo_echo","mode":"delay","delay_ms":5000}]}`); status != http.StatusNoContent {
		t.Fatalf("publish delay rule = %d", status)
	}
	delayed := make(chan error, 1)
	go func() {
		_, err := call()
		delayed <- err
	}()
	select {
	case err := <-delayed:
		if !isUnavailableDelivery(err, "unknown") && err == nil {
			t.Errorf("delayed call = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		// The delay is still holding the call, which is the point of the rule.
	}
	if status := faults(`{"faults":[]}`); status != http.StatusNoContent {
		t.Fatalf("clear fault rules = %d", status)
	}
	request, _ := http.NewRequest(http.MethodGet, "http://host"+processFaultsPath, nil)
	request.Header.Set("Authorization", "Bearer "+processLinkTestToken)
	response, err := processLinkClient(control).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var listed struct {
		Faults []processHostFault `json:"faults"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil || len(listed.Faults) != 0 {
		t.Fatalf("listed faults = %#v, %v", listed.Faults, err)
	}
}
