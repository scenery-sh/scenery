package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// A Go handler behind the worker Host reads the same HTTP request as it does
// in process, except for the credentials the Host consumed at ingress.
func TestWorkerHostForwardsIngressRequestToGoWorker(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	const process, operation, binding = "orders", "orders/operation/update", "orders/binding/update_http"
	identity := workerabi.Identity{ABI: workerabi.Version, Service: "orders/service/api", ContractRevision: "contract", ImplementationRevision: "build"}
	plan := WorkerServicePlan{
		Address: identity.Service, Module: "orders", Name: "api", Process: process, Runtime: "go",
		Operations: map[string]WorkerOperationPlan{
			operation: {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "std.type.unit"}},
		},
	}
	observed := make(chan shared.Request, 1)
	if err := RegisterWorkerOperation(operation, func(context.Context, []byte) ([]byte, shared.ByteStream, error) {
		observed <- *CurrentRequest()
		return []byte(`{"kind":"result","name":"ok","value":{}}`), shared.ByteStream{}, nil
	}); err != nil {
		t.Fatal(err)
	}

	directory, err := os.MkdirTemp("", "abi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "w")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	launch := workerabi.Launch{Identity: identity, Token: "test-token"}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: launch.Handler(invokeGoWorker)}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()
	client := workerabi.NewClient(socket, launch.Token, identity)
	defer client.Close()
	generation := &processHostGeneration{
		number: 1, lifetime: t.Context(),
		instances: map[string]*processHostInstance{
			process: {worker: client, spec: processGenerationInstance{
				Identity: processInstanceIdentity{ContractRevision: identity.ContractRevision},
			}},
		},
	}
	workers := &workerHost{host: &processHost{}, codec: contract.ManifestTypes(nil).Normalize}

	request := httptest.NewRequest(http.MethodPut, "/orders/42", nil)
	request.Header.Set("X-Correlation-Id", "corr-1")
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Cookie", "app_access=secret")
	state := newExternalState(&Endpoint{Service: plan.Name, Name: binding}, request, shared.PathParams{{Name: "id", Value: "42"}}, nil, AuthInfo{UID: "user-1"})
	ctx := context.WithValue(withState(t.Context(), state), processHostGenerationKey{}, generation)
	ctx = withRuntimeInvocation(ctx, state)
	if _, err := workers.invoke(ctx, plan, plan.Operations[operation], operation, binding, "direct", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	seen := <-observed
	if seen.Method != http.MethodPut || seen.Path != "/orders/42" || seen.PathParams.Get("id") != "42" ||
		seen.Headers.Get("X-Correlation-Id") != "corr-1" {
		t.Fatalf("request observed by the Go worker: %+v", seen)
	}
	if seen.Headers.Get("Authorization") != "" || seen.Headers.Get("Cookie") != "" {
		t.Fatalf("ingress credentials reached the worker: %v", seen.Headers)
	}
}

func TestProductionWorkerIngressNeutralityAndInternalPaths(t *testing.T) {
	routes := newRouteTable()
	routes.Handle([]string{http.MethodGet}, "/api/ping", func(w http.ResponseWriter, _ *http.Request, _ routeParams) {
		w.Header().Set("X-App-Generation", "1")
		w.Header().Set("X-App-Worker-Pid", "99")
		w.Header().Set("X-App-Worker-Revision", "rev-1")
		w.Header().Set("X-Scenery-Debug", "leak")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id, X-Scenery-Debug, X-App-Worker-Pid")
		w.WriteHeader(http.StatusOK)
	})
	host := &processHost{
		routes: routes, workers: &workerHost{}, developmentCall: false,
		current: &processHostGeneration{number: 1},
	}
	handler := neutralWorkerIngress(http.HandlerFunc(host.serveIngress), false)

	for _, path := range []string{"/__scenery/process/v1/generations", "/api/scenery/status"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/ping status = %d", rec.Code)
	}
	for _, forbidden := range []string{"X-App-Generation", "X-App-Worker-Pid", "X-App-Worker-Revision", "X-Scenery-Debug"} {
		if got := rec.Header().Get(forbidden); got != "" {
			t.Fatalf("production response leaked %s=%q", forbidden, got)
		}
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-Id" {
		t.Fatalf("Access-Control-Expose-Headers = %q, want X-Request-Id", got)
	}
}

func TestWorkerHostHTTPDurableMapsTransportStatuses(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	workers := &workerHost{host: &processHost{}, codec: contract.ManifestTypes(nil).Normalize}
	plan := WorkerServicePlan{
		Address: "orders/service/orders", Module: "orders", Name: "orders", Process: "orders", Runtime: "go",
		Operations: map[string]WorkerOperationPlan{
			"orders/operation/run": {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "std.type.unit"}},
		},
		Durable: map[string]WorkerDurablePlan{
			"orders/execution/run": {
				Operation: "orders/operation/run",
				Registration: ContractDurableRegistration{
					Address: "orders/execution/run", ExternalName: "orders.run",
				},
			},
		},
	}
	binding := WorkerBindingPlan{
		Address: "orders/binding/run_http", Operation: "orders/operation/run", Protocol: "http",
		Delivery: "enqueue", Execution: "orders/execution/run",
		Policy: ContractHTTPPolicy{
			ServiceName: "orders", OperationAddress: "orders/operation/run", BindingAddress: "orders/binding/run_http",
			AuthorizationStrategy: "public", TransportStatuses: map[string]int{"dispatch.rejected": http.StatusTooManyRequests},
		},
		HTTP: &WorkerHTTPPlan{
			Method: "POST", Path: "/api/run", Access: Public,
			Responses: map[string]WorkerResponsePlan{
				"dispatch.enqueued": {Status: http.StatusAccepted, Codec: "json", Media: []string{"application/json"}},
			},
		},
	}
	if err := workers.registerBinding(plan, binding); err != nil {
		t.Fatal(err)
	}
	endpoints := listEndpoints()
	if len(endpoints) != 1 || endpoints[0].Invoke == nil {
		t.Fatalf("unexpected endpoints: %+v", endpoints)
	}
	_, err := endpoints[0].Invoke(t.Context(), nil, json.RawMessage(`{}`))
	transportErr, ok := errors.AsType[*ContractTransportError](err)
	if !ok || transportErr.Outcome != "dispatch.rejected" || transportErr.Status != http.StatusTooManyRequests {
		t.Fatalf("durable error = %#v (%T), want dispatch.rejected HTTP 429", err, err)
	}
}

func TestWorkerHostRegistersAndDispatchesStreamingMCPTool(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	identity := workerabi.Identity{ABI: workerabi.Version, Service: "house/service/house", ContractRevision: "contract", ImplementationRevision: "impl"}
	directory, err := os.MkdirTemp("", "abi-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "w")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	launch := workerabi.Launch{Identity: identity, Token: "test-token"}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: launch.Handler(func(context.Context, workerabi.Invocation) (workerabi.Result, error) {
		return workerabi.Result{
			Outcome:    []byte(`{"kind":"result","name":"ok","value":{"name":"note.txt","content":""}}`),
			Stream:     io.NopCloser(strings.NewReader("hello")),
			StreamSize: 5,
		}, nil
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	client := workerabi.NewClient(socket, launch.Token, identity)
	defer client.Close()
	codec := contract.ManifestTypes{
		"house/record/Export": {
			Kind:   "record",
			Fields: []contract.ManifestField{{Name: "name", Type: "string"}, {Name: "content", Type: "bytes"}},
		},
	}.Normalize
	plan := WorkerServicePlan{
		Address: identity.Service, Module: "house", Name: "house", Process: "house", Runtime: "rust",
		Operations: map[string]WorkerOperationPlan{
			"house/operation/export": {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "house/record/Export"}},
		},
		MCPTools: []WorkerMCPToolPlan{{
			ID: "app/assistant/support#house/binding/export_mcp", Name: "house__export",
			AssistantAddress: "app/assistant/support", Binding: "house/binding/export_mcp",
			Operation: "house/operation/export", Execution: "house/execution/export_direct", Delivery: "call",
			Policy:       ContractHTTPPolicy{ServiceName: "house", OperationAddress: "house/operation/export", BindingAddress: "house/binding/export_mcp", AuthorizationStrategy: "public"},
			Limits:       MCPToolLimits{MaxInputBytes: 1024, MaxResultBytes: 1024},
			Approval:     "never",
			StreamFields: map[string][]string{"result.ok": {"content"}},
		}},
	}
	host := &processHost{
		contract: "contract", routes: newRouteTable(), closing: make(chan struct{}),
		generations: map[uint64]*processHostGeneration{},
	}
	if err := host.registerWorkerServices([]WorkerServicePlan{plan}, codec, nil, nil); err != nil {
		t.Fatal(err)
	}
	generation := &processHostGeneration{
		number: 1, lifetime: t.Context(),
		instances: map[string]*processHostInstance{"house": {worker: client, spec: processGenerationInstance{Identity: processInstanceIdentity{ContractRevision: "contract"}}}},
	}
	host.current = generation
	host.generations[1] = generation

	outcome, err := (processHostMCPDispatcher{host: host}).CallTool(t.Context(), MCPToolCallContext{
		Principal: "user-1", AssistantAddress: "app/assistant/support",
	}, "house__export", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Outcome != "ok" || string(outcome.Value) != `{"content":"aGVsbG8=","name":"note.txt"}` {
		t.Fatalf("unexpected MCP outcome: %+v (%s)", outcome, outcome.Value)
	}
	_ = errs.OK
}
