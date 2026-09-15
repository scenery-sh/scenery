package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
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
	if status := host.status(); status.Current != 2 || len(status.Generations) != 1 || status.Generations[0].Processes["echo_echo"] != 202 {
		t.Fatalf("host status = %#v", status)
	}
}

func isUnavailableDelivery(err error, delivery string) bool {
	typed, ok := errs.As(err)
	return ok && typed.Code == errs.Unavailable && typed.Meta["delivery"] == delivery
}

func TestProcessHostForwardsMCPToolsAndDurableReceiptsToTheirOwner(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	for pointer, value := range map[*string]string{
		&linkedContractRevision: processHostTestContract, &linkedImplementationRevision: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		&linkedBuildInputDigest: "sha256:3333333333333333333333333333333333333333333333333333333333333333", &linkedGoTarget: "development",
	} {
		previous := *pointer
		*pointer = value
		t.Cleanup(func() { *pointer = previous })
	}
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
	owner := &server{}
	socket := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case processMCPCallPath:
			owner.handleProcessMCPCall(w, req, nil)
		case processMCPDurablePath:
			owner.handleProcessMCPDurable(w, req, nil)
		default:
			http.NotFound(w, req)
		}
	}))
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "house_house", MCPTools: []ProcessHostMCPTool{
		{Process: "house_house", AssistantAddress: "app/assistant/support", Name: "house__process_scene"},
	}}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	bundle := CurrentLinkedContractBundle()
	if err := host.publish(processGenerationManifest{Generation: 1, ContractRevision: processHostTestContract, Processes: map[string]processGenerationInstance{
		"house_house": {Network: socket.Network, Address: socket.Address, PID: os.Getpid(), Identity: processInstanceIdentity(bundle)},
	}}); err != nil {
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
	other := call
	other.Principal = "principal-2"
	if _, err := durable.Status(context.Background(), other, "execution-1"); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("status for another principal = %v", err)
	}
	// The owner process authorizes and reads the receipt itself; without its
	// durable store the forwarded status reports the owner's own failure.
	if _, err := durable.Status(context.Background(), call, "execution-1"); err == nil || !strings.Contains(err.Error(), "durable execution store is unavailable") {
		t.Fatalf("forwarded durable status = %v", err)
	}
}

func TestProcessDrainStopsBackgroundWorkWithTheSessionToken(t *testing.T) {
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: processLinkTarget{Network: "unix", Address: "/unused"}})
	drained := 0
	setProcessBackgroundDrain(func(context.Context) error { drained++; return nil })
	t.Cleanup(func() { setProcessBackgroundDrain(nil) })
	owner := &server{}
	for token, want := range map[string]int{"wrong-token-wrong-token-wrong-token": http.StatusUnauthorized, processLinkTestToken: http.StatusNoContent} {
		request := httptest.NewRequest(http.MethodPost, processDrainPath, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		owner.handleProcessDrain(recorder, request, nil)
		if recorder.Code != want {
			t.Fatalf("drain with token %q = %d, want %d", token, recorder.Code, want)
		}
	}
	if drained != 1 {
		t.Fatalf("background drain ran %d times", drained)
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
