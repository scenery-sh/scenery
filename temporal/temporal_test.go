package temporal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
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

func TestNewActivityValidatesConfig(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	for _, tt := range []struct {
		name string
		cfg  ActivityConfig
		want string
	}{
		{
			name: "missing task queue",
			cfg:  ActivityConfig{},
			want: "task queue must not be empty",
		},
		{
			name: "negative timeout",
			cfg:  ActivityConfig{TaskQueue: "orders.go", StartToClose: -time.Second},
			want: "StartToClose cannot be negative",
		},
		{
			name: "negative concurrency",
			cfg:  ActivityConfig{TaskQueue: "orders.go", MaxConcurrency: -1},
			want: "MaxConcurrency cannot be negative",
		},
		{
			name: "missing backoff coefficient",
			cfg: ActivityConfig{TaskQueue: "orders.go", RetryPolicy: RetryPolicy{
				InitialInterval: time.Second,
				MaximumAttempts: 2,
			}},
			want: "BackoffCoefficient must be set",
		},
		{
			name: "maximum before initial",
			cfg: ActivityConfig{TaskQueue: "orders.go", RetryPolicy: RetryPolicy{
				InitialInterval:    time.Minute,
				BackoffCoefficient: 2,
				MaximumInterval:    time.Second,
			}},
			want: "MaximumInterval cannot be less than InitialInterval",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				got := recover()
				if got == nil || !strings.Contains(fmt.Sprint(got), tt.want) {
					t.Fatalf("panic = %v, want %q", got, tt.want)
				}
			}()
			_ = NewActivity("orders.Invalid/v1", tt.cfg, func(context.Context, testWorkflowInput) (testWorkflowOutput, error) {
				return testWorkflowOutput{}, nil
			})
		})
	}
}

func TestStartWorkflowOptionsApplyOverrides(t *testing.T) {
	wf := &Workflow[testWorkflowInput, testWorkflowOutput]{
		name:   "orders.Fulfill/v1",
		config: WorkflowConfig{TaskQueue: "orders.go", WorkflowExecutionTimeout: time.Minute},
	}
	opts, err := startWorkflowOptions(
		onlavaruntime.TemporalRuntimeInfo{DeploymentName: "orders", WorkerBuildID: "api-build"},
		wf,
		WorkflowID("orders.123"),
		WithTaskQueue("priority.go"),
		WithMemo(map[string]any{"kind": "test"}),
		WithSearchAttributes(map[string]any{"CustomKeywordField": "demo"}),
		WithPinnedBuildID("worker-build"),
		WithWorkflowIDConflictPolicy(WorkflowIDConflictUseExisting),
		WithWorkflowIDReusePolicy(WorkflowIDReuseRejectDuplicate),
	)
	if err != nil {
		t.Fatalf("startWorkflowOptions returned error: %v", err)
	}
	if opts.ID != "orders.123" || opts.TaskQueue != "priority.go" {
		t.Fatalf("opts identity = %q/%q", opts.ID, opts.TaskQueue)
	}
	if opts.Memo["kind"] != "test" || opts.SearchAttributes["CustomKeywordField"] != "demo" {
		t.Fatalf("opts metadata = %#v/%#v", opts.Memo, opts.SearchAttributes)
	}
	if opts.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Fatalf("conflict policy = %v", opts.WorkflowIDConflictPolicy)
	}
	if opts.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatalf("reuse policy = %v", opts.WorkflowIDReusePolicy)
	}
	if opts.VersioningOverride == nil {
		t.Fatal("expected pinned versioning override")
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
