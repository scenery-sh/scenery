package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/devreport"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
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

func useProcessLinkRegistryForTest(t *testing.T) {
	t.Helper()
	previous := global
	global = &registry{contractBindings: map[string]ContractInternalBindingRegistration{}}
	t.Cleanup(func() { global = previous })
}

// serveProcessLinkForTest serves handler on a private Unix socket; the short
// temporary root keeps the socket path within platform limits.
func serveProcessLinkForTest(t *testing.T, handler http.Handler) processLinkTarget {
	t.Helper()
	directory, err := os.MkdirTemp("", "spl")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(directory, "owner.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = os.RemoveAll(directory) })
	return processLinkTarget{Network: "unix", Address: socket}
}

func processLinkOwnerHandler() http.Handler {
	owner := &server{}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { owner.handleProcessLinkedBinding(w, req, nil) })
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
	valid := write("valid.json", `{"token":"`+processLinkTestToken+`","dispatch":{"network":"unix","address":"/tmp/host.sock"}}`)
	config, err := readProcessLink(valid)
	if err != nil || config.Dispatch.Address != "/tmp/host.sock" {
		t.Fatalf("valid process link = %#v, %v", config, err)
	}
	for name, content := range map[string]string{
		"short-token":      `{"token":"short","dispatch":{"network":"unix","address":"/tmp/host.sock"}}`,
		"missing-dispatch": `{"token":"` + processLinkTestToken + `"}`,
		"bad-network":      `{"token":"` + processLinkTestToken + `","dispatch":{"network":"udp","address":"x"}}`,
		"empty-address":    `{"token":"` + processLinkTestToken + `","dispatch":{"network":"unix","address":""}}`,
		"unknown-field":    `{"token":"` + processLinkTestToken + `","dispatch":{"network":"unix","address":"/tmp/host.sock"},"extra":true}`,
	} {
		if _, err := readProcessLink(write(name+".json", content)); err == nil {
			t.Errorf("%s process link was accepted", name)
		}
	}
	previous := processLinkState
	processLinkState = &processLinkRuntime{clients: map[processLinkTarget]*http.Client{}}
	t.Cleanup(func() { processLinkState = previous })
	t.Setenv("SCENERY_PROCESS_LINK", write("broken.json", `{"token":"short"}`))
	if err := Main(AppConfig{Name: "linked"}); err == nil || !strings.Contains(err.Error(), "process link") || processLinkConfigured() {
		t.Fatalf("runtime startup with invalid process wiring = %v", err)
	}
}

func TestProcessLinkedCallMatchesInProcessSemantics(t *testing.T) {
	useProcessLinkRegistryForTest(t)
	defer setTestReporter(&devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 64)})()
	target := serveProcessLinkForTest(t, processLinkOwnerHandler())
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: target}
	useProcessLinkForTest(t, config)
	type observation struct {
		UID, Tenant, Roles, Principal, InvocationTenant                  string
		Type, Method, Path, Service, Endpoint, Header, InvocationID      string
		RequestTrace, SpanTrace, SpanParent, CallerBinding, DeadlineText string
	}
	observed := make(chan observation, 1)
	if err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		Address: "echo/binding/whoami", Visibility: "application",
		Policy: &ContractHTTPPolicy{BindingAddress: "echo/binding/whoami", AuthorizationStrategy: "deny_unless_allowed", AuthorizationRuleCount: 1, AuthorizationRules: []ContractAuthorizationRule{
			{Name: "member", Expression: `principal.authenticated && context.tenant_id == "tenant-1" && contains(principal.roles, "member")`},
		}},
		DecodeInput:  func(data []byte) (any, error) { return string(data), nil },
		EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
		Invoke: func(ctx context.Context, _, _ any) (any, error) {
			auth, request := CurrentAuth(), appsdk.CurrentRequest()
			invocation, _ := runtimeapi.InvocationFromContext(ctx)
			data, _ := auth.Data.(map[string]any)
			child, span := appsdk.StartSpan(ctx, "lookup")
			span.End(nil)
			value := observation{
				UID: auth.UID, Tenant: fmt.Sprint(data["tenant_id"]), Roles: fmt.Sprint(data["roles"]), Principal: invocation.Principal(), InvocationTenant: invocation.TenantID(),
				Type: string(request.Type), Method: request.Method, Path: request.Path, Service: request.Service, Endpoint: request.Endpoint,
				Header: request.Headers.Get("X-Request-Tag"), InvocationID: request.InvocationID, RequestTrace: request.TraceID, CallerBinding: request.CallerBinding,
				DeadlineText: request.Deadline.UTC().Format(time.RFC3339Nano),
			}
			if state := stateFromContext(child); state != nil && state.trace != nil {
				value.SpanTrace, value.SpanParent = state.trace.traceID, state.trace.parentSpanID
			}
			observed <- value
			return "ok", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)
	call := func(data any, remote bool) (observation, error) {
		started := time.Now().UTC()
		state := &requestState{started: started, logsEnabled: true, traceEnabled: true, auth: AuthInfo{UID: "user-42", Data: data},
			request: shared.Request{Type: shared.APICall, Started: started, InvocationID: "invocation-7", TraceID: "trace-7", CallerBinding: "greeter/binding/greet_http",
				Deadline: deadline, Service: "greeter", Endpoint: "GreetHttp", Method: "POST", Path: "/greet", Headers: http.Header{"X-Request-Tag": {"parity"}}},
			trace: &traceSpan{traceID: "trace-7", spanID: "span-greet", isRoot: true}}
		ctx := withRuntimeInvocation(withState(context.Background(), state), state)
		restore := enterState(state)
		defer restore()
		var err error
		if remote {
			invocation, _ := runtimeapi.InvocationFromContext(ctx)
			_, err = invokeProcessLinkedBindingJSON(ctx, config, "echo/binding/whoami", "greeter", invocation, []byte(`"hi"`))
		} else {
			_, err = InvokeContractBindingJSON(ctx, "echo/binding/whoami", "greeter", []byte(`"hi"`))
		}
		if err != nil {
			return observation{}, err
		}
		return <-observed, nil
	}
	member := map[string]any{"tenant_id": "tenant-1", "roles": []string{"member"}}
	local, localErr := call(member, false)
	remote, remoteErr := call(member, true)
	if localErr != nil || remoteErr != nil || local != remote || local.UID != "user-42" || local.InvocationTenant != "tenant-1" || local.Header != "parity" || local.SpanTrace != "trace-7" || local.SpanParent != "span-greet" {
		t.Fatalf("in-process %#v (%v) differs from process-linked %#v (%v)", local, localErr, remote, remoteErr)
	}
	guest := map[string]any{"tenant_id": "tenant-1", "roles": []string{"guest"}}
	if _, localErr = call(guest, false); errs.Code(localErr) != errs.PermissionDenied {
		t.Fatalf("in-process guest call = %v", localErr)
	}
	if _, remoteErr = call(guest, true); errs.Code(remoteErr) != errs.PermissionDenied || remoteErr.Error() != localErr.Error() {
		t.Fatalf("process-linked guest call = %v, in-process %v", remoteErr, localErr)
	}
	if _, remoteErr = call(struct{ Role string }{Role: "member"}, true); remoteErr == nil || !strings.Contains(fmt.Sprint(errors.Unwrap(remoteErr)), "cannot cross a process boundary") {
		t.Fatalf("unregistered authentication data type = %v", remoteErr)
	}
}

func TestProcessLinkedBindingCallerForwardsInvocationAndRestoresErrors(t *testing.T) {
	useProcessLinkRegistryForTest(t)
	target := serveProcessLinkForTest(t, processLinkOwnerHandler())
	crashed := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	started, stopped := make(chan struct{}, 1), make(chan error, 1)
	failures := map[string]error{
		"echo/binding/transport": ContractSystemError(fmt.Errorf("query: %w", context.DeadlineExceeded)),
		"echo/binding/errs":      errs.B().Code(errs.NotFound).Msg("contact not found").Err(),
		"echo/binding/canceled":  fmt.Errorf("stopped: %w", context.Canceled),
		"echo/binding/nested":    &errs.Error{Code: errs.NotFound, Message: "outer", Cause: ContractSystemError(errs.B().Code(errs.Aborted).Msg("inner").Err())},
	}
	register := func(address string, invoke ContractInternalInvoke) {
		if err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
			Address: address, Visibility: "application", Invoke: invoke,
			DecodeInput:  func(data []byte) (any, error) { return string(data), nil },
			EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
		}); err != nil {
			t.Fatal(err)
		}
	}
	register("echo/binding/echo_internal", func(context.Context, any, any) (any, error) {
		return map[string]string{"kind": "result", "name": "ok"}, nil
	})
	for address, failure := range failures {
		register(address, func(context.Context, any, any) (any, error) { return nil, failure })
	}
	register("echo/binding/wait", func(ctx context.Context, _, _ any) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- ctx.Err()
		return nil, ctx.Err()
	})
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: target}
	useProcessLinkForTest(t, config)
	dispatchTargets := map[string]processLinkTarget{"echo/binding/crash": crashed, "echo/binding/missing": {Network: "unix", Address: filepath.Join(t.TempDir(), "gone.sock")}}
	invoke := func(ctx context.Context, address string) ([]byte, error) {
		invocation, _ := runtimeapi.InvocationFromContext(ctx)
		called := config
		if dispatch, ok := dispatchTargets[address]; ok {
			called = &processLinkConfig{Token: processLinkTestToken, Dispatch: dispatch}
		}
		return invokeProcessLinkedBindingJSON(ctx, called, address, "greeter", invocation, []byte(`"hi"`))
	}
	ctx := runtimeapi.WithInvocation(context.Background(), runtimeapi.NewInvocation("invocation-2", "user-2", "", "", time.Time{}))
	if output, err := invoke(ctx, "echo/binding/echo_internal"); err != nil || string(output) != `{"kind":"result","name":"ok"}` {
		t.Fatalf("linked output = %s, %v", output, err)
	}
	_, err := invoke(ctx, "echo/binding/transport")
	if transport, ok := errors.AsType[*ContractTransportError](err); !ok || transport.Outcome != "system.internal" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transport error = %#v", err)
	}
	if _, err = invoke(ctx, "echo/binding/errs"); errs.Code(err) != errs.NotFound || err.Error() != "contact not found" {
		t.Fatalf("typed error = %#v", err)
	}
	if _, err = invoke(ctx, "echo/binding/canceled"); !errors.Is(err, context.Canceled) || err.Error() != "stopped: context canceled" {
		t.Fatalf("callee cancellation = %#v", err)
	}
	_, err = invoke(ctx, "echo/binding/nested")
	if outer, ok := errs.As(err); !ok || outer.Code != errs.NotFound || outer.Message != "outer" {
		t.Fatalf("nested outer failure = %#v", err)
	}
	if transport, ok := errors.AsType[*ContractTransportError](err); !ok || transport.Outcome != "system.internal" || errs.Code(transport.Cause) != errs.Aborted {
		t.Fatalf("nested inner failures = %#v", err)
	}
	for address, delivery := range map[string]string{"echo/binding/missing": "not_sent", "echo/binding/crash": "unknown"} {
		_, err = invoke(ctx, address)
		if typed, ok := errs.As(err); !ok || typed.Code != errs.Unavailable || typed.Meta["delivery"] != delivery {
			t.Fatalf("%s failure = %#v", address, err)
		}
	}
	expiring := runtimeapi.WithInvocation(context.Background(), runtimeapi.NewInvocation("invocation-3", "user-2", "", "", time.Now().Add(20*time.Millisecond)))
	if _, err = invoke(expiring, "echo/binding/wait"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline = %#v", err)
	}
	<-started
	if calleeErr := <-stopped; !errors.Is(calleeErr, context.DeadlineExceeded) && !errors.Is(calleeErr, context.Canceled) {
		t.Fatalf("callee after expired deadline = %v", calleeErr)
	}
	canceled, cancel := context.WithCancel(ctx)
	go func() { <-started; cancel() }()
	if _, err = invoke(canceled, "echo/binding/wait"); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation = %#v", err)
	}
	select {
	case calleeErr := <-stopped:
		if !errors.Is(calleeErr, context.Canceled) {
			t.Fatalf("callee after caller cancellation = %v", calleeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("callee did not observe caller cancellation")
	}
	if _, err = InvokeContractBindingJSON(ctx, "echo/binding/unlinked", "greeter", nil); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unowned binding = %v", err)
	}
	encode := func(value any) ([]byte, error) { return json.Marshal(map[string]any{"message": value}) }
	decode := func(data []byte) (any, error) { return "decoded:" + string(data), nil }
	global.mu.Lock()
	delete(global.contractBindings, "echo/binding/echo_internal")
	global.mu.Unlock()
	invocation, _ := runtimeapi.InvocationFromContext(ctx)
	if value, err := InvokeContractBindingCodec(ctx, "echo/binding/echo_internal", "greeter", invocation, "hi", encode, decode); err == nil || value != nil {
		t.Fatalf("codec call to a binding its owner no longer registers = %#v, %v", value, err)
	}
	other := runtimeapi.NewInvocation("invocation-4", "user-2", "", "", time.Time{})
	if _, err = InvokeContractBindingCodec(ctx, "echo/binding/errs", "greeter", other, "hi", encode, decode); err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("codec call with foreign invocation = %v", err)
	}
}

func TestProcessLinkedBindingOwnerRejectsUnauthenticatedOrUnownedCalls(t *testing.T) {
	useProcessLinkRegistryForTest(t)
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken})
	if err := RegisterContractInternalBinding("echo/binding/echo_internal", func(context.Context, any, any) (any, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}
	target := serveProcessLinkForTest(t, processLinkOwnerHandler())
	for _, check := range []struct {
		token, address string
		status         int
	}{
		{token: "wrong-token-wrong-token-wrong-token", address: "echo/binding/echo_internal", status: http.StatusUnauthorized},
		{token: processLinkTestToken, address: "missing/binding/x", status: http.StatusNotFound},
	} {
		config := &processLinkConfig{Token: check.token, Dispatch: target}
		invocation := runtimeapi.NewInvocation("invocation-5", "", "", "", time.Time{})
		_, err := invokeProcessLinkedBindingJSON(runtimeapi.WithInvocation(context.Background(), invocation), config, check.address, "greeter", invocation, []byte(`"hi"`))
		if err == nil {
			t.Fatalf("%s call with token %q was accepted", check.address, check.token)
		}
	}
}

func TestForwardedRequestGenerationPinsItsInternalCalls(t *testing.T) {
	useProcessLinkRegistryForTest(t)
	seen := make(chan string, 1)
	dispatch := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen <- req.Header.Get(processGenerationHeader) + " " + req.Header.Get(processLinkBindingHeader)
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: json.RawMessage(`"ok"`)})
	}))
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: dispatch}
	useProcessLinkForTest(t, config)
	endpoint := &Endpoint{Service: "greeter", Name: "GreetHttp", Access: Public, Path: "/greet", Methods: []string{"POST"}}
	handler := withProcessGeneration(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(processGenerationHeader) != "" {
			t.Error("process generation header reached the application request")
		}
		state := newExternalState(endpoint, req, nil, nil, AuthInfo{UID: "user-1"})
		ctx := withRuntimeInvocation(withState(req.Context(), state), state)
		restore := enterState(state)
		defer restore()
		if _, err := InvokeContractBindingJSON(ctx, "echo/binding/echo_internal", "greeter", []byte(`{}`)); err != nil {
			t.Error(err)
		}
	}))
	request := httptest.NewRequest(http.MethodPost, "/greet", nil)
	request.Header.Set(processGenerationHeader, "7")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if got := <-seen; got != "7 echo/binding/echo_internal" {
		t.Fatalf("dispatched call = %q", got)
	}
	invalid := httptest.NewRequest(http.MethodPost, "/greet", nil)
	invalid.Header.Set(processGenerationHeader, "latest")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid generation status = %d", recorder.Code)
	}
}
