package temporal

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"

	sceneryruntime "scenery.sh/runtime"
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
		return testWorkflowOutput(in), nil
	})
	act := NewActivity("payments.Capture/v1", ActivityConfig{TaskQueue: "payments.go", StartToClose: time.Minute}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput(in), nil
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

func TestNewExternalActivityDoesNotRegisterWorkerDeclaration(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	act := NewExternalActivity[testWorkflowInput, testWorkflowOutput]("payments.Render/v1", ActivityConfig{TaskQueue: "payments.ts", StartToClose: time.Minute})
	if act.Name() != "payments.Render/v1" || act.Config().TaskQueue != "payments.ts" {
		t.Fatalf("external activity = %q %+v", act.Name(), act.Config())
	}
	if len(snapshotDeclarations()) != 0 {
		t.Fatalf("external activity should not register Go worker declaration: %#v", snapshotDeclarations())
	}
}

func TestActivityConfigUnkeyedLiteralCompatibility(t *testing.T) {
	cfg := ActivityConfig{"orders.legacy.go", time.Minute, 2, RetryPolicy{}}
	if cfg.TaskQueue != "orders.legacy.go" || cfg.StartToClose != time.Minute || cfg.MaxConcurrency != 2 {
		t.Fatalf("unkeyed ActivityConfig = %#v", cfg)
	}
}

func TestActivityHeartbeatOption(t *testing.T) {
	act := NewExternalActivity[testWorkflowInput, testWorkflowOutput](
		"payments.Heartbeat/v1",
		ActivityConfig{TaskQueue: "payments.ts"},
		WithHeartbeatTimeout(5*time.Second),
	)
	if got := act.temporalActivityOptions().HeartbeatTimeout; got != 5*time.Second {
		t.Fatalf("HeartbeatTimeout = %s, want 5s", got)
	}
}

func TestActivityHeartbeatOptionRejectsNegative(t *testing.T) {
	defer func() {
		got := recover()
		if got == nil || !strings.Contains(fmt.Sprint(got), "HeartbeatTimeout cannot be negative") {
			t.Fatalf("panic = %v, want HeartbeatTimeout validation", got)
		}
	}()
	_ = NewExternalActivity[testWorkflowInput, testWorkflowOutput](
		"payments.NegativeHeartbeat/v1",
		ActivityConfig{TaskQueue: "payments.ts"},
		WithHeartbeatTimeout(-time.Second),
	)
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

func TestNewActivityRequiresTaskQueue(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "task queue") {
			t.Fatalf("expected task queue panic, got %v", r)
		}
	}()
	_ = NewActivity("payments.Capture/v1", ActivityConfig{}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
}

func TestTemporalDefaultWorkerTaskQueue(t *testing.T) {
	info := sceneryruntime.TemporalRuntimeInfo{TaskQueuePrefix: "scenery.orders"}
	if got := defaultWorkerTaskQueue(info); got != "scenery.orders.worker.go" {
		t.Fatalf("defaultWorkerTaskQueue = %q", got)
	}
	if got := defaultWorkerTaskQueue(sceneryruntime.TemporalRuntimeInfo{}); got != "scenery.worker.go" {
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

	opts := temporalWorkerOptionsForQueue(sceneryruntime.TemporalRuntimeInfo{TaskQueuePrefix: "scenery.orders", HostReporting: true}, "worker", "orders.go", snapshotDeclarations())
	if opts.MaxConcurrentActivityExecutionSize != 1 {
		t.Fatalf("MaxConcurrentActivityExecutionSize = %d", opts.MaxConcurrentActivityExecutionSize)
	}
	if opts.SysInfoProvider == nil {
		t.Fatal("expected SysInfoProvider")
	}
}

func TestTemporalDeclarationsUseSessionScopedTaskQueues(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{TaskQueue: "orders.go"}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	_ = NewActivity("payments.Capture/v1", ActivityConfig{TaskQueue: "payments.go"}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})

	byQueue := declarationsByQueueForTest(sceneryruntime.TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders.session-a",
		SessionID:       "session-a",
	})
	want := []string{"scenery.orders.session-a.orders.go", "scenery.orders.session-a.payments.go"}
	if got := sortedDeclarationQueues(byQueue); !reflect.DeepEqual(got, want) {
		t.Fatalf("queues = %#v, want %#v", got, want)
	}
}

func TestStartWorkerRuntimeSkipsEmptyAndAPIRole(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	stop, err := startWorkerRuntime(context.Background(), sceneryruntime.AppConfig{Role: "worker"})
	if err != nil {
		t.Fatalf("empty startWorkerRuntime error = %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("empty stop error = %v", err)
	}

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	stop, err = startWorkerRuntime(context.Background(), sceneryruntime.AppConfig{Role: "api"})
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
	_, err := startWorkerRuntime(context.Background(), sceneryruntime.AppConfig{Role: "worker"})
	if err == nil || !strings.Contains(err.Error(), "temporal.enabled") {
		t.Fatalf("expected temporal.enabled error, got %v", err)
	}
}

func TestSelectedTemporalTaskQueuesFromEnv(t *testing.T) {
	t.Setenv("SCENERY_TEMPORAL_TASK_QUEUE", "orders.go, payments.go, orders.go,  ")
	got := selectedTemporalTaskQueuesFromEnv()
	want := []string{"orders.go", "payments.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectedTemporalTaskQueuesFromEnv = %#v, want %#v", got, want)
	}
}

func TestScopedSelectedTemporalTaskQueues(t *testing.T) {
	got := scopedSelectedTemporalTaskQueues(sceneryruntime.TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders.session-a",
		SessionID:       "session-a",
	}, []string{"orders.go", "scenery.orders.session-a.payments.go"})
	want := []string{"scenery.orders.session-a.orders.go", "scenery.orders.session-a.payments.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scopedSelectedTemporalTaskQueues = %#v, want %#v", got, want)
	}
}

func TestFilterDeclarationsBySelectedTaskQueues(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{TaskQueue: "orders.go"}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	_ = NewActivity("payments.Capture/v1", ActivityConfig{TaskQueue: "payments.go"}, func(ctx context.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	byQueue := declarationsByQueueForTest(sceneryruntime.TemporalRuntimeInfo{TaskQueuePrefix: "scenery.orders"})

	all, err := filterDeclarationsBySelectedTaskQueues(byQueue, nil)
	if err != nil {
		t.Fatalf("filter without selection returned error: %v", err)
	}
	if got := sortedDeclarationQueues(all); !reflect.DeepEqual(got, []string{"orders.go", "payments.go"}) {
		t.Fatalf("all queues = %#v", got)
	}

	one, err := filterDeclarationsBySelectedTaskQueues(byQueue, []string{"payments.go"})
	if err != nil {
		t.Fatalf("filter one returned error: %v", err)
	}
	if got := sortedDeclarationQueues(one); !reflect.DeepEqual(got, []string{"payments.go"}) {
		t.Fatalf("one queue = %#v", got)
	}

	multi, err := filterDeclarationsBySelectedTaskQueues(byQueue, []string{" payments.go ", "orders.go", "payments.go"})
	if err != nil {
		t.Fatalf("filter multiple returned error: %v", err)
	}
	if got := sortedDeclarationQueues(multi); !reflect.DeepEqual(got, []string{"orders.go", "payments.go"}) {
		t.Fatalf("multiple queues = %#v", got)
	}
}

func TestFilterDeclarationsBySelectedTaskQueuesRejectsUnknownQueue(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	_ = NewWorkflow("orders.Fulfill/v1", WorkflowConfig{TaskQueue: "orders.go"}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	byQueue := declarationsByQueueForTest(sceneryruntime.TemporalRuntimeInfo{TaskQueuePrefix: "scenery.orders"})

	_, err := filterDeclarationsBySelectedTaskQueues(byQueue, []string{"missing.go"})
	if err == nil || !strings.Contains(err.Error(), "missing.go") || !strings.Contains(err.Error(), "orders.go") {
		t.Fatalf("expected unknown queue error with declared queues, got %v", err)
	}
}

func TestStartRejectsNilWorkflowWithoutRuntime(t *testing.T) {
	_, err := Start[testWorkflowInput, testWorkflowOutput](context.Background(), nil, testWorkflowInput{}, WorkflowID("orders-123"))
	if err == nil || !strings.Contains(err.Error(), "nil workflow") {
		t.Fatalf("expected nil workflow error, got %v", err)
	}
}

func TestStartRejectsZeroWorkflowIdentity(t *testing.T) {
	restore := resetRegistryForTest()
	defer restore()

	wf := NewWorkflow("orders.Fulfill/v1", WorkflowConfig{TaskQueue: "orders.go"}, func(ctx workflow.Context, in testWorkflowInput) (testWorkflowOutput, error) {
		return testWorkflowOutput{}, nil
	})
	_, err := Start(context.Background(), wf, testWorkflowInput{}, WorkflowIdentity{})
	if err == nil || !strings.Contains(err.Error(), "WorkflowID") {
		t.Fatalf("expected explicit workflow id error, got %v", err)
	}
}

func TestWorkflowIdentityConstructorsRejectEmptyValues(t *testing.T) {
	for name, fn := range map[string]func(){
		"WorkflowID":       func() { _ = WorkflowID(" ") },
		"WorkflowIDPrefix": func() { _ = WorkflowIDPrefix(" ") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic")
				}
			}()
			fn()
		})
	}
}

func TestTemporalStartWorkflowOptions(t *testing.T) {
	customKeyword := NewSearchAttributeKeyKeyword("CustomKeywordField")
	opts := applyStartOptions(WorkflowConfig{
		TaskQueue:                "cfg.go",
		WorkflowExecutionTimeout: time.Hour,
	}, WithTaskQueue("orders.go"), WithMemo(map[string]any{"tenant": "acme"}), WithSearchAttributes(NewSearchAttributes(customKeyword.ValueSet("orders"))), WithRunTimeout(time.Minute), WithTaskTimeout(10*time.Second), WithPinnedBuildID("build-2"), WithWorkflowIDConflictPolicy(WorkflowIDConflictUseExisting), WithWorkflowIDReusePolicy(WorkflowIDReuseRejectDuplicate))

	info := sceneryruntime.TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders",
		DeploymentName:  "orders-deployment",
	}
	start := temporalStartWorkflowOptions("orders.Fulfill/v1", WorkflowID("orders-123"), opts, "default.go", info)
	if start.ID != "orders-123" || start.TaskQueue != "orders.go" {
		t.Fatalf("start identity = %q queue %q", start.ID, start.TaskQueue)
	}
	if start.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING || start.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatalf("workflow id policies = %s %s", start.WorkflowIDConflictPolicy, start.WorkflowIDReusePolicy)
	}
	if start.WorkflowExecutionTimeout != time.Hour || start.WorkflowRunTimeout != time.Minute || start.WorkflowTaskTimeout != 10*time.Second {
		t.Fatalf("timeouts = %s %s %s", start.WorkflowExecutionTimeout, start.WorkflowRunTimeout, start.WorkflowTaskTimeout)
	}
	gotKeyword, ok := start.TypedSearchAttributes.GetKeyword(customKeyword)
	if start.Memo["tenant"] != "acme" || !ok || gotKeyword != "orders" || start.SearchAttributes != nil {
		t.Fatalf("metadata = %#v deprecated=%#v typed=%#v", start.Memo, start.SearchAttributes, start.TypedSearchAttributes.GetUntypedValues())
	}
	pinned, ok := start.VersioningOverride.(*temporalclient.PinnedVersioningOverride)
	if !ok {
		t.Fatalf("VersioningOverride = %T", start.VersioningOverride)
	}
	if pinned.Version.DeploymentName != "orders-deployment" || pinned.Version.BuildID != "build-2" {
		t.Fatalf("pinned version = %#v", pinned.Version)
	}

	start.Memo["tenant"] = "mutated"
	gotOptKeyword, ok := opts.SearchAttributes.GetKeyword(customKeyword)
	if opts.Memo["tenant"] != "acme" || !ok || gotOptKeyword != "orders" {
		t.Fatalf("start options should clone metadata maps")
	}
}

func TestTemporalStartWorkflowOptionsDoNotPinByDefault(t *testing.T) {
	opts := applyStartOptions(WorkflowConfig{TaskQueue: "cfg.go"})
	start := temporalStartWorkflowOptions("orders.Fulfill/v1", WorkflowIDPrefix("orders"), opts, "default.go", sceneryruntime.TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders",
		DeploymentName:  "orders-deployment",
		WorkerBuildID:   "build-1",
	})
	if start.TaskQueue != "cfg.go" {
		t.Fatalf("TaskQueue = %q", start.TaskQueue)
	}
	if start.VersioningOverride != nil {
		t.Fatalf("VersioningOverride = %#v, want nil", start.VersioningOverride)
	}
}

func TestTemporalStartWorkflowOptionsUseSessionScopedTaskQueue(t *testing.T) {
	opts := applyStartOptions(WorkflowConfig{
		TaskQueue: "cfg.go",
	}, WithTaskQueue("orders.go"))
	start := temporalStartWorkflowOptions("orders.Fulfill/v1", WorkflowID("orders-123"), opts, "default.go", sceneryruntime.TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders.session-a",
		SessionID:       "session-a",
	})
	if start.TaskQueue != "scenery.orders.session-a.orders.go" {
		t.Fatalf("TaskQueue = %q", start.TaskQueue)
	}

	opts = applyStartOptions(WorkflowConfig{})
	start = temporalStartWorkflowOptions("orders.Fulfill/v1", WorkflowID("orders-123"), opts, "scenery.orders.session-a.worker.go", sceneryruntime.TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders.session-a",
		SessionID:       "session-a",
	})
	if start.TaskQueue != "scenery.orders.session-a.worker.go" {
		t.Fatalf("default TaskQueue = %q", start.TaskQueue)
	}
}

func TestGetWorkflowWithoutRuntimeReturnsHandleError(t *testing.T) {
	run := GetWorkflow[testWorkflowOutput](context.Background(), "orders-123", "")
	if run.ID() != "orders-123" {
		t.Fatalf("ID = %q", run.ID())
	}
	if _, err := run.Get(context.Background()); err == nil || !strings.Contains(err.Error(), "runtime is not started") {
		t.Fatalf("expected runtime error from Get, got %v", err)
	}
	if err := run.Cancel(context.Background()); err == nil || !strings.Contains(err.Error(), "runtime is not started") {
		t.Fatalf("expected runtime error from Cancel, got %v", err)
	}
	if err := run.Terminate(context.Background(), "test"); err == nil || !strings.Contains(err.Error(), "runtime is not started") {
		t.Fatalf("expected runtime error from Terminate, got %v", err)
	}
	if err := Signal(context.Background(), run, "approve", testWorkflowInput{}); err == nil || !strings.Contains(err.Error(), "runtime is not started") {
		t.Fatalf("expected runtime error from Signal, got %v", err)
	}
	if _, err := Query[testWorkflowOutput, testWorkflowOutput](context.Background(), run, "state"); err == nil || !strings.Contains(err.Error(), "runtime is not started") {
		t.Fatalf("expected runtime error from Query, got %v", err)
	}
	if _, err := Update[testWorkflowInput, testWorkflowOutput, testWorkflowOutput](context.Background(), run, "update", testWorkflowInput{}); err == nil || !strings.Contains(err.Error(), "runtime is not started") {
		t.Fatalf("expected runtime error from Update, got %v", err)
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

func declarationsByQueueForTest(info sceneryruntime.TemporalRuntimeInfo) map[string][]declaration {
	byQueue := make(map[string][]declaration)
	for _, item := range snapshotDeclarations() {
		queue := item.taskQueue(info)
		byQueue[queue] = append(byQueue[queue], item)
	}
	return byQueue
}

func sortedDeclarationQueues(byQueue map[string][]declaration) []string {
	queues := make([]string, 0, len(byQueue))
	for queue := range byQueue {
		queues = append(queues, queue)
	}
	sort.Strings(queues)
	return queues
}
