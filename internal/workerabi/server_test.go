package workerabi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerHandlerAttestsBeforeDispatch(t *testing.T) {
	identity := Identity{ABI: Version, Service: "quote/service/api", ContractRevision: "contract", ImplementationRevision: "implementation"}
	launch := Launch{Token: "private-test-token", Identity: identity}
	deadline := time.Now().Add(time.Minute).UTC()
	invocation := Invocation{Service: identity.Service, Operation: "quote/operation/price", Mode: "direct",
		Context: InvocationContext{ContractRevision: "wrong", InvocationID: "request", Scope: "scope", Deadline: &deadline},
		Input:   json.RawMessage(`{"amount":"9007199254740993"}`),
	}
	calls := 0
	handler := launch.Handler(func(ctx context.Context, call Invocation) (Result, error) {
		calls++
		actual, ok := ctx.Deadline()
		if !ok || !actual.Equal(deadline) || string(call.Input) != string(invocation.Input) {
			t.Fatalf("worker received deadline %v and input %s", actual, call.Input)
		}
		if call.Mode == "durable" {
			return Result{}, errors.New("private handler error")
		}
		return Result{Outcome: json.RawMessage(`{"kind":"result","name":"ok","value":{}}`), Stream: io.NopCloser(strings.NewReader("bytes")), StreamSize: 5}, nil
	})
	invoke := func(token string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(invocation)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, InvokePath, strings.NewReader(string(data)))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("invoke returned %d, want %d", response.Code, wantStatus)
		}
		if token == launch.Token && (response.Header().Get(ContractHeader) != identity.ContractRevision ||
			response.Header().Get(ImplementationHeader) != identity.ImplementationRevision) {
			t.Fatal("authenticated response has no worker attestation")
		}
		return response
	}
	invoke("wrong token", http.StatusUnauthorized)
	invoke(launch.Token, http.StatusConflict)
	if calls != 0 {
		t.Fatal("rejected invocation reached the worker handler")
	}
	invocation.Context.ContractRevision = identity.ContractRevision
	response := invoke(launch.Token, http.StatusOK)
	result, err := (&Client{identity: identity}).readInvocationResult(response.Result())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Stream.Close() }()
	data, err := io.ReadAll(result.Stream)
	if err != nil || string(data) != "bytes" || result.StreamSize != 5 {
		t.Fatalf("invalid framed stream %q (size %d): %v", data, result.StreamSize, err)
	}
	invocation.Mode = "durable"
	fault := invoke(launch.Token, http.StatusInternalServerError)
	if strings.Contains(fault.Body.String(), "private") || calls != 2 {
		t.Fatalf("unexpected fault: %s", fault.Body)
	}
}
