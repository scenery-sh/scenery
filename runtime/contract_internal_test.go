package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

func TestContractInternalBindingIsExactAndDuplicateSafe(t *testing.T) {
	previous := global
	global = &registry{contractBindings: map[string]ContractInternalBindingRegistration{}}
	t.Cleanup(func() { global = previous })
	if err := RegisterContractInternalBinding("house/binding/get", func(_ context.Context, invocation, input any) (any, error) {
		return []any{invocation, input}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterContractInternalBinding("house/binding/get", func(context.Context, any, any) (any, error) { return nil, nil }); err == nil {
		t.Fatal("duplicate internal binding was accepted")
	}
	invocation := runtimeapi.NewInvocation("invocation-1", "principal", "", "", time.Time{})
	ctx := runtimeapi.WithInvocation(context.Background(), invocation)
	value, err := InvokeContractBinding(ctx, "house/binding/get", invocation, "input")
	if err != nil {
		t.Fatal(err)
	}
	items := value.([]any)
	if items[0] != invocation || items[1] != "input" {
		t.Fatalf("value = %#v", value)
	}
	if _, err := InvokeContractBinding(context.Background(), "house/binding/get", runtimeapi.Invocation{}, "input"); err == nil {
		t.Fatal("forged internal invocation was accepted")
	}
	if _, err := InvokeContractBinding(context.Background(), "missing", nil, nil); err == nil {
		t.Fatal("missing internal binding was accepted")
	}
}

func TestContractInternalBindingEnforcesVisibilityAuthorizationAndPipeline(t *testing.T) {
	previous := global
	global = &registry{contractBindings: map[string]ContractInternalBindingRegistration{}}
	t.Cleanup(func() { global = previous })
	invocation := runtimeapi.NewInvocation("invocation-policy", "principal", "", "", time.Time{})
	ctx := runtimeapi.WithInvocation(context.Background(), invocation)
	if err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		Address: "house/binding/package", Visibility: "package", Package: "house",
		Policy: &ContractHTTPPolicy{BindingAddress: "house/binding/package", AuthorizationStrategy: "public", PipelineSteps: []string{"std.middleware.trace", "std.middleware.recover"}},
		Invoke: func(context.Context, any, any) (any, error) { return "ok", nil },
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := InvokeContractBindingFrom(ctx, "house/binding/package", "other", invocation, nil); err == nil || !strings.Contains(err.Error(), "visible only") {
		t.Fatalf("package visibility error = %v", err)
	}
	value, err := InvokeContractBindingFrom(ctx, "house/binding/package", "house", invocation, nil)
	if err != nil || value != "ok" {
		t.Fatalf("package invocation = %#v, %v", value, err)
	}
	if err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		Address: "house/binding/panic", Visibility: "application",
		Policy: &ContractHTTPPolicy{BindingAddress: "house/binding/panic", AuthorizationStrategy: "public", PipelineSteps: []string{"std.middleware.recover"}},
		Invoke: func(context.Context, any, any) (any, error) { panic("broken") },
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := InvokeContractBindingFrom(ctx, "house/binding/panic", "house", invocation, nil); err == nil {
		t.Fatal("pipeline recovery returned no error")
	} else {
		var transport *ContractTransportError
		if !errors.As(err, &transport) || transport.Error() != "contract implementation failure" || transport.Cause == nil || !strings.Contains(transport.Cause.Error(), "panic in contract invocation") {
			t.Fatalf("pipeline recovery error = %#v", err)
		}
	}
}

func TestRuntimeInvocationCarriesTrustedContextMetadata(t *testing.T) {
	deadline := time.Now().UTC().Add(time.Minute).Round(0)
	state := &requestState{
		request: shared.Request{
			InvocationID: "invocation-1", TraceID: "trace-1", CallerBinding: "house/binding/process",
			ExecutionID: "job-1", Deployment: "app/deployment/preview", Locale: "en-GB", Deadline: deadline,
		},
		auth: AuthInfo{UID: "user-1", Data: map[string]any{"tenant_id": "tenant-1"}},
	}
	ctx := withRuntimeInvocation(context.Background(), state)
	invocation, ok := runtimeapi.InvocationFromContext(ctx)
	if !ok {
		t.Fatal("runtime invocation is missing")
	}
	gotDeadline, hasDeadline := invocation.Deadline()
	if invocation.ID() != "invocation-1" || invocation.Principal() != "user-1" || invocation.TenantID() != "tenant-1" || invocation.TraceID() != "trace-1" || invocation.CallerBinding() != "house/binding/process" || invocation.ExecutionID() != "job-1" || invocation.Deployment() != "app/deployment/preview" || invocation.Locale() != "en-GB" || !hasDeadline || !gotDeadline.Equal(deadline) {
		t.Fatalf("invocation metadata was not preserved: id=%q principal=%q tenant=%q trace=%q caller=%q execution=%q deployment=%q locale=%q deadline=%v/%t", invocation.ID(), invocation.Principal(), invocation.TenantID(), invocation.TraceID(), invocation.CallerBinding(), invocation.ExecutionID(), invocation.Deployment(), invocation.Locale(), gotDeadline, hasDeadline)
	}
}

func TestContractDurableRegistrationCarriesExactRevisionAndReceipt(t *testing.T) {
	previous := global
	global = &registry{durableTasks: map[string]*DurableTask{}, contractDurableExecutions: map[string]ContractDurableRegistration{}}
	t.Cleanup(func() { global = previous })

	registration := ContractDurableRegistration{
		Address: "house/execution/process", ExternalName: "house.Process/v1", EngineAddress: "app/execution_engine/tasks", Service: "house", Revision: 7,
		DefaultTimeout: 40 * time.Minute, DefaultLease: 20 * time.Minute, MaxAttempts: 6,
		RetryInitial: 10 * time.Second, RetryMax: 2 * time.Minute, RetryBackoff: 2,
		SuccessRetention: 7 * 24 * time.Hour, FailureRetention: 30 * 24 * time.Hour,
		MaxConcurrency: 2, DeduplicationRetention: 24 * time.Hour, DeduplicationConflict: "return_existing",
		Handler: func(context.Context, []byte) ([]byte, error) { return []byte(`{}`), nil },
	}
	if err := RegisterContractDurableExecution(registration); err != nil {
		t.Fatal(err)
	}
	if err := RegisterContractDurableExecution(registration); err == nil {
		t.Fatal("duplicate durable execution was accepted")
	}
	tasks := listDurableTasks()
	if len(tasks) != 1 || tasks[0].Name != registration.ExternalName || tasks[0].HandlerRef != registration.Address || tasks[0].Version != 7 || tasks[0].DefaultTimeout != 40*time.Minute || tasks[0].SuccessRetention != 7*24*time.Hour || tasks[0].MaxConcurrency != 2 || tasks[0].DeduplicationRetention != 24*time.Hour {
		t.Fatalf("tasks = %#v", tasks)
	}
	receipt := contractExecutionReceipt(registration, "job-42")
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"durable_identity":"app/execution_engine/tasks::house/execution/process","execution_id":"job-42","accepted_revision":"7"}`
	if string(encoded) != want {
		t.Fatalf("receipt = %s, want %s", encoded, want)
	}

	invalid := registration
	invalid.Address = "house/execution/invalid"
	invalid.EngineAddress = ""
	if err := RegisterContractDurableExecution(invalid); err == nil {
		t.Fatal("durable execution without engine was accepted")
	}
}

func TestContractDurableFailureOutcomeIsStable(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{contractDurableFailure("dispatch.rejected", context.Canceled), "dispatch.rejected"},
		{contractDurableFailure("dispatch.wait_timeout", context.DeadlineExceeded), "dispatch.wait_timeout"},
		{context.Canceled, "system.internal"},
	} {
		if got := ContractDurableFailureOutcome(test.err); got != test.want {
			t.Errorf("outcome(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
