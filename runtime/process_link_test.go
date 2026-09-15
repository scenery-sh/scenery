package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
)

const processLinkTestToken = "0123456789abcdef0123456789abcdef"

func useProcessLinkForTest(t *testing.T, config *processLinkConfig) {
	t.Helper()
	previous := processLinkState
	state := &processLinkRuntime{config: config, clients: map[processLinkTarget]*http.Client{}}
	state.once.Do(func() {})
	processLinkState = state
	t.Cleanup(func() { processLinkState = previous })
}

func TestProcessLinkConfigurationRejectsWeakOrMalformedLinks(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := write("valid.json", `{"token":"`+processLinkTestToken+`","bindings":{"echo/binding/echo_internal":{"network":"unix","address":"/tmp/echo.sock"}}}`)
	config, err := readProcessLink(valid)
	if err != nil || config.Bindings["echo/binding/echo_internal"].Address != "/tmp/echo.sock" {
		t.Fatalf("valid process link = %#v, %v", config, err)
	}
	for name, content := range map[string]string{
		"short-token":   `{"token":"short","bindings":{}}`,
		"bad-network":   `{"token":"` + processLinkTestToken + `","bindings":{"a":{"network":"udp","address":"x"}}}`,
		"empty-address": `{"token":"` + processLinkTestToken + `","bindings":{"a":{"network":"unix","address":""}}}`,
		"unknown-field": `{"token":"` + processLinkTestToken + `","bindings":{},"extra":true}`,
	} {
		if _, err := readProcessLink(write(name+".json", content)); err == nil {
			t.Errorf("%s process link was accepted", name)
		}
	}
}

func TestProcessLinkedBindingOwnerInvokesWithCallerInvocation(t *testing.T) {
	previous := global
	global = &registry{contractBindings: map[string]ContractInternalBindingRegistration{}}
	t.Cleanup(func() { global = previous })
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken})
	deadline := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)
	if err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		Address: "echo/binding/echo_internal", Visibility: "application",
		DecodeInput:  func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
		Invoke: func(ctx context.Context, invocation, input any) (any, error) {
			current, ok := runtimeapi.InvocationFromContext(ctx)
			got, _ := current.Deadline()
			if !ok || !runtimeapi.SameInvocation(current, invocation.(runtimeapi.Invocation)) || current.Principal() != "user-1" || current.TenantID() != "tenant-1" || !got.Equal(deadline) {
				return nil, errors.New("caller invocation was not propagated")
			}
			return map[string]any{"echo": input}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{}
	call := func(token, address string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(processLinkRequest{Address: address, CallerPackage: "greeter", Input: json.RawMessage(`"hi"`),
			Invocation: processLinkInvocation{ID: "invocation-1", Principal: "user-1", TenantID: "tenant-1", Deadline: &deadline}})
		request := httptest.NewRequest(http.MethodPost, processLinkBindingPath, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		s.handleProcessLinkedBinding(recorder, request, nil)
		return recorder
	}
	recorder := call(processLinkTestToken, "echo/binding/echo_internal")
	var response processLinkResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK || response.Error != nil || string(response.Output) != `{"echo":"\"hi\""}` {
		t.Fatalf("owner response = %d %s: %v", recorder.Code, recorder.Body.String(), err)
	}
	if recorder := call("wrong-token-wrong-token-wrong-token", "echo/binding/echo_internal"); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", recorder.Code)
	}
	if recorder := call(processLinkTestToken, "missing/binding/x"); recorder.Code != http.StatusNotFound {
		t.Fatalf("unowned binding status = %d", recorder.Code)
	}
}

func TestProcessLinkedBindingCallerForwardsInvocationAndRestoresErrors(t *testing.T) {
	previous := global
	global = &registry{contractBindings: map[string]ContractInternalBindingRegistration{}}
	t.Cleanup(func() { global = previous })
	directory, err := os.MkdirTemp("", "spl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "echo.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	failures := map[string]error{
		"echo/binding/transport": ContractSystemError(errors.New("database unavailable")),
		"echo/binding/errs":      errs.B().Code(errs.NotFound).Msg("contact not found").Err(),
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body processLinkRequest
		if req.Header.Get("Authorization") != "Bearer "+processLinkTestToken || json.NewDecoder(req.Body).Decode(&body) != nil ||
			body.CallerPackage != "greeter" || body.Invocation.ID != "invocation-2" || body.Invocation.Principal != "user-2" || string(body.Input) != `{"message":"hi"}` {
			writeProcessLinkResponse(w, http.StatusBadRequest, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "unexpected request"}})
			return
		}
		if failure := failures[body.Address]; failure != nil {
			writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(failure)})
			return
		}
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: json.RawMessage(`{"kind":"result","name":"ok"}`)})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	target := processLinkTarget{Network: "unix", Address: socket}
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Bindings: map[string]processLinkTarget{
		"echo/binding/echo_internal": target, "echo/binding/transport": target, "echo/binding/errs": target,
	}})
	ctx := runtimeapi.WithInvocation(context.Background(), runtimeapi.NewInvocation("invocation-2", "user-2", "", "", time.Time{}))
	output, err := InvokeContractBindingJSON(ctx, "echo/binding/echo_internal", "greeter", []byte(`{"message":"hi"}`))
	if err != nil || string(output) != `{"kind":"result","name":"ok"}` {
		t.Fatalf("linked output = %s, %v", output, err)
	}
	_, err = InvokeContractBindingJSON(ctx, "echo/binding/transport", "greeter", []byte(`{"message":"hi"}`))
	var transport *ContractTransportError
	if !errors.As(err, &transport) || transport.Outcome != "system.internal" || transport.Cause == nil || !strings.Contains(transport.Cause.Error(), "database unavailable") {
		t.Fatalf("transport error = %#v", err)
	}
	if _, err = InvokeContractBindingJSON(ctx, "echo/binding/errs", "greeter", []byte(`{"message":"hi"}`)); errs.Code(err) != errs.NotFound || err.Error() != "contact not found" {
		t.Fatalf("typed error = %#v", err)
	}
	if _, err = InvokeContractBindingJSON(context.Background(), "echo/binding/echo_internal", "greeter", nil); err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("call without invocation = %v", err)
	}
	if _, err = InvokeContractBindingJSON(ctx, "unlinked/binding/x", "greeter", nil); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unlinked binding = %v", err)
	}
}
