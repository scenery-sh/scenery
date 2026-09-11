package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scenery.sh/auth"
	"scenery.sh/internal/nativeprotocol"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/runtimeapp"
)

const testToken = "private-test-channel-token-0123456789"

func workerTestHandler(t *testing.T, invoke func(context.Context, any) (any, runtimeapp.ByteStream, error)) *Handler {
	t.Helper()
	registry := NewRegistry()
	if err := registry.RegisterService(NativeServiceRegistration{Address: "app/service/native", Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterOperation(Operation{Address: "app/operation/read", Service: "app/service/native", Decode: func(data []byte) (any, error) {
		var value struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		return &value, nil
	}, Encode: json.Marshal, Invoke: invoke}); err != nil {
		t.Fatal(err)
	}
	if err := registry.seal(); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(registry, testToken, "contract", "inputs", []Admission{{Operation: "app/operation/read", Binding: "app/binding/read"}})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func workerTestRequest() InvocationRequest {
	return InvocationRequest{Protocol: Protocol, ProtocolRevision: nativeprotocol.Revision, ContractRevision: "contract", InputDigest: "inputs", Operation: "app/operation/read", Input: json.RawMessage(`{"name":"native"}`), Context: RequestContext{InvocationID: "invocation", CallerBinding: "app/binding/read", Principal: "user", Auth: &nativeprotocol.StandardAuth{UserID: "user", TenantID: "tenant", ActorUserID: "actor", SessionID: "session", ImpersonationID: "impersonation"}}}
}

func TestWorkerRetainsNativeContextAndConcreteFailure(t *testing.T) {
	type key struct{}
	marker := &struct{ value string }{"pointer"}
	want := errors.New("native concrete failure")
	handler := workerTestHandler(t, func(ctx context.Context, input any) (any, runtimeapp.ByteStream, error) {
		if ctx.Value(key{}) != marker {
			t.Fatal("native context value changed")
		}
		invocation, ok := runtimeapi.InvocationFromContext(ctx)
		if !ok || invocation.Principal() != "user" || invocation.TenantID() != "tenant" {
			t.Fatal("worker did not create its trusted invocation")
		}
		data, ok := auth.CurrentAuthData()
		if !ok || data.SessionID != "session" || data.ImpersonationID != "impersonation" || string(data.ActorUserID) != "actor" {
			t.Fatalf("standard auth native type was lost: %#v", data)
		}
		if runtimeapp.CurrentRequest().Payload != input {
			t.Fatal("request lost its native payload pointer")
		}
		return nil, runtimeapp.ByteStream{}, want
	})
	_, err := handler.invoke(context.WithValue(context.Background(), key{}, marker), workerTestRequest())
	if err != want { //nolint:errorlint // The in-process callback must preserve the concrete error identity.
		t.Fatalf("native error identity changed: %v", err)
	}
	if runtimeapp.CurrentAuth() != nil || runtimeapp.CurrentRequest().Type != "none" {
		t.Fatal("request scope leaked")
	}
}

func TestWorkerProtocolRejectsUntrustedOrUnadmittedCalls(t *testing.T) {
	calls := 0
	handler := workerTestHandler(t, func(context.Context, any) (any, runtimeapp.ByteStream, error) {
		calls++
		return map[string]string{"result": "ok"}, runtimeapp.ByteStream{}, nil
	})
	for _, test := range []struct {
		name   string
		token  string
		modify func(*InvocationRequest)
		status int
	}{
		{"missing token", "", func(*InvocationRequest) {}, http.StatusUnauthorized},
		{"wrong protocol revision", testToken, func(request *InvocationRequest) { request.ProtocolRevision = "other" }, http.StatusConflict},
		{"wrong identity", testToken, func(request *InvocationRequest) { request.InputDigest = "old" }, http.StatusConflict},
		{"unknown operation", testToken, func(request *InvocationRequest) { request.Operation = "other" }, http.StatusNotImplemented},
		{"wrong binding", testToken, func(request *InvocationRequest) { request.Context.CallerBinding = "other" }, http.StatusNotImplemented},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocation := workerTestRequest()
			test.modify(&invocation)
			body, err := json.Marshal(invocation)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/invoke", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+test.token)
			writer := httptest.NewRecorder()
			handler.ServeHTTP(writer, request)
			if writer.Code != test.status {
				t.Fatalf("status %d, want %d", writer.Code, test.status)
			}
		})
	}
	if calls != 0 {
		t.Fatal("rejected invocation executed native code")
	}
	invocation := workerTestRequest()
	invocation.Context.Deadline = time.Now().Add(-time.Second)
	if _, err := handler.invoke(context.Background(), invocation); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	invocation = workerTestRequest()
	invocation.Context.Auth.UserID = "forged"
	if _, err := handler.invoke(context.Background(), invocation); err == nil {
		t.Fatal("mismatched standard identity was accepted")
	}
	if calls != 0 {
		t.Fatal("invalid context executed native code")
	}
}

func TestWorkerWireFailureIsSanitizedAndRequestScopeIsReleased(t *testing.T) {
	handler := workerTestHandler(t, func(context.Context, any) (any, runtimeapp.ByteStream, error) { panic("private database details") })
	body, err := json.Marshal(workerTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/invoke", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	if !strings.Contains(writer.Body.String(), "native_execution_failed") || strings.Contains(writer.Body.String(), "private") {
		t.Fatalf("unsafe response: %s", writer.Body.String())
	}
	if runtimeapp.CurrentAuth() != nil {
		t.Fatal("panic leaked request scope")
	}
}

func TestWorkerCannotAdmitStreamingAsBufferedUnary(t *testing.T) {
	handler := workerTestHandler(t, func(context.Context, any) (any, runtimeapp.ByteStream, error) {
		t.Fatal("stream ran")
		return nil, runtimeapp.ByteStream{}, nil
	})
	handler.registry.mu.Lock()
	operation := handler.registry.operations["app/operation/read"]
	operation.Streaming = true
	handler.registry.operations[operation.Address] = operation
	handler.registry.mu.Unlock()
	if _, err := NewHandler(handler.registry, testToken, "contract", "inputs", []Admission{{Operation: operation.Address, Binding: "app/binding/read"}}); err == nil {
		t.Fatal("stream admitted without a streaming transport")
	}
}
