package temporal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/workflow"

	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

type testWorkflowInput struct {
	Value string
}

type testWorkflowOutput struct {
	Value string
}

func TestNewWorkflowAndActivityRegisterDeclarations(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	wf := NewWorkflow("orders.Fulfill/v1", WorkflowConfig{TaskQueue: "orders.go"}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{Value: in.Value}, nil
	})
	act := NewActivity("payments.Capture/v1", ActivityConfig{TaskQueue: "payments.go", StartToClose: time.Minute}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{Value: in.Value}, nil
	})

	if wf.Name() != "orders.Fulfill/v1" || wf.Config().TaskQueue != "orders.go" {
		t.Fatalf("workflow = %q %+v", wf.Name(), wf.Config())
	}
	if act.Name() != "payments.Capture/v1" || act.Config().TaskQueue != "payments.go" {
		t.Fatalf("activity = %q %+v", act.Name(), act.Config())
	}
	items := snapshotDeclarations()
	if len(items) != 2 {
		t.Fatalf("declarations = %#v", items)
	}
}

func TestWorkflowVersioningBehaviorConversion(t *testing.T) {
	if workflowVersioningBehavior(VersioningDefault) != workflow.VersioningBehaviorUnspecified {
		t.Fatal("default behavior should leave registration behavior unspecified")
	}
	if workflowVersioningBehavior(VersioningPinned) != workflow.VersioningBehaviorPinned {
		t.Fatal("pinned behavior mismatch")
	}
	if workflowVersioningBehavior(VersioningAutoUpgrade) != workflow.VersioningBehaviorAutoUpgrade {
		t.Fatal("auto-upgrade behavior mismatch")
	}
}

func TestTemporalDeclarationsRejectDuplicates(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected duplicate workflow declaration panic")
		}
	}()
	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
}

func TestTemporalDefaultWorkerTaskQueue(t *testing.T) {
	info := onlavaruntime.TemporalRuntimeInfo{TaskQueuePrefix: "onlava.orders"}
	if got := defaultWorkerTaskQueue(info); got != "onlava.orders.worker.go" {
		t.Fatalf("defaultWorkerTaskQueue = %q", got)
	}
	if got := defaultWorkerTaskQueue(onlavaruntime.TemporalRuntimeInfo{}); got != "onlava.worker.go" {
		t.Fatalf("defaultWorkerTaskQueue empty = %q", got)
	}
}

func TestTemporalWorkerOptionsForQueueUsesSmallestActivityConcurrency(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{TaskQueue: "orders.go"}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	_ = NewActivity("orders.Fast/v1", ActivityConfig{TaskQueue: "orders.go", MaxConcurrency: 8}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	_ = NewActivity("orders.Serial/v1", ActivityConfig{TaskQueue: "orders.go", MaxConcurrency: 1}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})

	opts := temporalWorkerOptionsForQueue(onlavaruntime.TemporalRuntimeInfo{TaskQueuePrefix: "onlava.orders"}, "worker", "orders.go", snapshotDeclarations())
	if opts.MaxConcurrentActivityExecutionSize != 1 {
		t.Fatalf("MaxConcurrentActivityExecutionSize = %d", opts.MaxConcurrentActivityExecutionSize)
	}
}

func TestStartWorkerRuntimeSkipsEmptyAndAPIRole(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	stop, err := startWorkerRuntime(context.Background(), onlavaruntime.AppConfig{Role: "worker"})
	if err != nil {
		t.Fatalf("empty startWorkerRuntime error = %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("empty stop error = %v", err)
	}

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	stop, err = startWorkerRuntime(context.Background(), onlavaruntime.AppConfig{Role: "api"})
	if err != nil {
		t.Fatalf("api startWorkerRuntime error = %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("api stop error = %v", err)
	}
}

func TestStartWorkerRuntimeRequiresTemporalRuntimeForDeclarations(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	_, err := startWorkerRuntime(context.Background(), onlavaruntime.AppConfig{Role: "worker"})
	if err == nil || !strings.Contains(err.Error(), "temporal.enabled") {
		t.Fatalf("expected temporal.enabled error, got %v", err)
	}
}

func TestStartRejectsNilWorkflowWithoutRuntime(t *testing.T) {
	_, err := Start[testWorkflowInput, testWorkflowOutput](context.Background(), nil, testWorkflowInput{})
	if err == nil || !strings.Contains(err.Error(), "nil workflow") {
		t.Fatalf("expected nil workflow error, got %v", err)
	}
}

func TestRetryPolicyConversion(t *testing.T) {
	if retryPolicy(RetryPolicy{}) != nil {
		t.Fatal("zero retry policy should not set Temporal retry policy")
	}
	policy := retryPolicy(RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.5,
		MaximumInterval:        time.Minute,
		MaximumAttempts:        5,
		NonRetryableErrorTypes: []string{"bad_request"},
	})
	if policy == nil || policy.InitialInterval != time.Second || policy.BackoffCoefficient != 2.5 || policy.MaximumAttempts != 5 {
		t.Fatalf("policy = %#v", policy)
	}
	policy.NonRetryableErrorTypes[0] = "mutated"
	if retryPolicy(RetryPolicy{NonRetryableErrorTypes: []string{"bad_request"}}).NonRetryableErrorTypes[0] != "bad_request" {
		t.Fatal("retry policy should copy non-retryable error types")
	}
}

type methodActivityService struct {
	value string
}

func TestMethodActivityResolvesRegisteredServiceAccessor(t *testing.T) {
	restore := resetServiceAccessorsForTest()
	defer restore()

	RegisterServiceAccessorFor[*methodActivityService](func() (any, error) {
		return &methodActivityService{value: "ok"}, nil
	})
	handler := MethodActivity[string]((*methodActivityService).handle)
	out, err := handler(context.Background(), "input")
	if err != nil {
		t.Fatalf("MethodActivity returned error: %v", err)
	}
	if out != (Void{}) {
		t.Fatalf("MethodActivity output = %#v", out)
	}
}

func TestMethodActivityRequiresRegisteredServiceAccessor(t *testing.T) {
	restore := resetServiceAccessorsForTest()
	defer restore()

	handler := MethodActivity[string]((*methodActivityService).handle)
	_, err := handler(context.Background(), "input")
	if err == nil || !strings.Contains(err.Error(), "no service accessor registered") {
		t.Fatalf("expected missing accessor error, got %v", err)
	}
}

func (s *methodActivityService) handle(ctx context.Context, value string) error {
	if s.value != "ok" || value != "input" {
		return errors.New("unexpected method activity input")
	}
	return nil
}

func resetRegistryForTest() func() {
	registry.mu.Lock()
	prevItems := registry.items
	prevNames := registry.names
	registry.items = nil
	registry.names = make(map[string]struct{})
	registry.mu.Unlock()
	return func() {
		registry.mu.Lock()
		registry.items = prevItems
		registry.names = prevNames
		registry.mu.Unlock()
	}
}

func resetServiceAccessorsForTest() func() {
	serviceAccessors.mu.Lock()
	prev := serviceAccessors.items
	serviceAccessors.items = make(map[string]func() (any, error))
	serviceAccessors.mu.Unlock()
	return func() {
		serviceAccessors.mu.Lock()
		serviceAccessors.items = prev
		serviceAccessors.mu.Unlock()
	}
}
