package host

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"scenery.sh/auth"
	"scenery.sh/internal/nativeprotocol"
	"scenery.sh/runtime/shared"
)

type workerRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip workerRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestNativeWorkerClientExportsOnlyCurrentStandardAuth(t *testing.T) {
	client, err := NewNativeWorkerClient("http://127.0.0.1:10000", "private-channel-token-0123456789012", "contract", "inputs")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.client.Transport = workerRoundTrip(func(request *http.Request) (*http.Response, error) {
		calls++
		var value nativeprotocol.InvocationRequest
		if err := json.NewDecoder(request.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		if value.Context.Principal != "user" || value.Context.Auth == nil || value.Context.Auth.TenantID != "tenant" || value.Context.Auth.SessionID != "session" {
			t.Fatalf("wrong principal transfer: %#v", value.Context)
		}
		if value.Context.CallerBinding != "binding" || value.Context.InvocationID != "invocation" || string(value.Input) != `{"tenant_id":"untrusted-path"}` {
			t.Fatalf("wrong request: %#v", value)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"outcome":{"kind":"result","name":"success","value":{}}}`)), Header: make(http.Header)}, nil
	})
	state := &requestState{request: shared.Request{InvocationID: "invocation", CallerBinding: "binding"}, auth: AuthInfo{UID: "user", Data: &auth.AuthData{UserID: "user", TenantID: "tenant", SessionID: "session"}}}
	ctx := withRuntimeInvocation(withState(context.Background(), state), state)
	if _, err := client.Invoke(ctx, "operation", json.RawMessage(`{"tenant_id":"untrusted-path"}`)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("native transport was not invoked")
	}
	state.auth.Data = map[string]string{"tenant_id": "tenant"}
	if _, err := client.Invoke(ctx, "operation", json.RawMessage(`{}`)); err == nil {
		t.Fatal("custom auth data was silently coerced")
	}
	if _, err := client.Invoke(context.Background(), "operation", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing runtime authority was accepted")
	}
	if calls != 1 {
		t.Fatal("untrusted call reached the transport")
	}
}

func TestNativeWorkerClientRejectsNonlocalTargetsAndTrailingResponses(t *testing.T) {
	for _, endpoint := range []string{"http://example.com:80", "http://127.0.0.1:80/other", "http://user@127.0.0.1:80", "https://127.0.0.1:80"} {
		if _, err := NewNativeWorkerClient(endpoint, "private-channel-token-0123456789012", "contract", "inputs"); err == nil {
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	client, err := NewNativeWorkerClient("http://127.0.0.1:10000", "private-channel-token-0123456789012", "contract", "inputs")
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = workerRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"outcome":{}} {"outcome":{}}`))}, nil
	})
	state := &requestState{request: shared.Request{InvocationID: "invocation", CallerBinding: "binding"}}
	ctx := withRuntimeInvocation(withState(context.Background(), state), state)
	if _, err := client.Invoke(ctx, "operation", json.RawMessage(`{}`)); err == nil {
		t.Fatal("accepted a trailing response")
	}
}
