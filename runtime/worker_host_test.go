package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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
