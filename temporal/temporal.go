package temporal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	temporalclient "go.temporal.io/sdk/client"
	temporalerror "go.temporal.io/sdk/temporal"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

type WorkflowContext = workflow.Context

type WorkflowConfig struct {
	TaskQueue                string
	WorkflowID               string
	WorkflowIDPrefix         string
	VersioningBehavior       VersioningBehavior
	WorkflowExecutionTimeout time.Duration
	WorkflowRunTimeout       time.Duration
	WorkflowTaskTimeout      time.Duration
}

type ActivityConfig struct {
	TaskQueue      string
	StartToClose   time.Duration
	MaxConcurrency int
	RetryPolicy    RetryPolicy
}

type RetryPolicy struct {
	InitialInterval        time.Duration
	BackoffCoefficient     float64
	MaximumInterval        time.Duration
	MaximumAttempts        int32
	NonRetryableErrorTypes []string
}

type WorkflowIDConflictPolicy enumspb.WorkflowIdConflictPolicy

const (
	WorkflowIDConflictUnspecified       WorkflowIDConflictPolicy = WorkflowIDConflictPolicy(enumspb.WORKFLOW_ID_CONFLICT_POLICY_UNSPECIFIED)
	WorkflowIDConflictFail              WorkflowIDConflictPolicy = WorkflowIDConflictPolicy(enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL)
	WorkflowIDConflictUseExisting       WorkflowIDConflictPolicy = WorkflowIDConflictPolicy(enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING)
	WorkflowIDConflictTerminateExisting WorkflowIDConflictPolicy = WorkflowIDConflictPolicy(enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING)
)

type WorkflowIDReusePolicy enumspb.WorkflowIdReusePolicy

const (
	WorkflowIDReuseUnspecified              WorkflowIDReusePolicy = WorkflowIDReusePolicy(enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED)
	WorkflowIDReuseAllowDuplicate           WorkflowIDReusePolicy = WorkflowIDReusePolicy(enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE)
	WorkflowIDReuseAllowDuplicateFailedOnly WorkflowIDReusePolicy = WorkflowIDReusePolicy(enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY)
	WorkflowIDReuseRejectDuplicate          WorkflowIDReusePolicy = WorkflowIDReusePolicy(enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE)
	WorkflowIDReuseTerminateIfRunning       WorkflowIDReusePolicy = WorkflowIDReusePolicy(enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING)
)

type VersioningBehavior string

const (
	VersioningDefault     VersioningBehavior = ""
	VersioningPinned      VersioningBehavior = "pinned"
	VersioningAutoUpgrade VersioningBehavior = "auto_upgrade"
)

type Void struct{}

type Workflow[I, O any] struct {
	name    string
	config  WorkflowConfig
	handler func(workflow.Context, I) (O, error)
}

type Activity[I, O any] struct {
	name    string
	config  ActivityConfig
	handler func(context.Context, I) (O, error)
}

type Run[O any] struct {
	run temporalclient.WorkflowRun
}

type ActivityFuture[O any] struct {
	future workflow.Future
}

type StartOption func(*startOptions)

type startOptions struct {
	id                       string
	idPrefix                 string
	taskQueue                string
	memo                     map[string]any
	searchAttributes         map[string]any
	workflowExecutionTimeout time.Duration
	workflowRunTimeout       time.Duration
	workflowTaskTimeout      time.Duration
	versioningOverride       temporalclient.VersioningOverride
	pinnedBuildID            string
	workflowIDConflictPolicy WorkflowIDConflictPolicy
	workflowIDReusePolicy    WorkflowIDReusePolicy
}

type declaration interface {
	declarationKey() string
	taskQueue(onlavaruntime.TemporalRuntimeInfo) string
	maxConcurrentActivityExecutions() int
	register(temporalworker.Worker)
}

var registry = struct {
	mu    sync.RWMutex
	items []declaration
	names map[string]struct{}
}{
	names: make(map[string]struct{}),
}

var serviceAccessors = struct {
	mu    sync.RWMutex
	items map[string]func() (any, error)
}{
	items: make(map[string]func() (any, error)),
}

func init() {
	onlavaruntime.RegisterTemporalWorkerStarter(startWorkerRuntime)
}

func NewWorkflow[I, O any](name string, cfg WorkflowConfig, handler func(workflow.Context, I) (O, error)) *Workflow[I, O] {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("temporal: workflow name must not be empty")
	}
	if handler == nil {
		panic("temporal: workflow handler must not be nil")
	}
	w := &Workflow[I, O]{name: name, config: cfg, handler: handler}
	registerDeclaration(w)
	return w
}

func NewActivity[I, O any](name string, cfg ActivityConfig, handler func(context.Context, I) (O, error)) *Activity[I, O] {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("temporal: activity name must not be empty")
	}
	if handler == nil {
		panic("temporal: activity handler must not be nil")
	}
	if err := validateActivityConfig(name, cfg); err != nil {
		panic(err.Error())
	}
	a := &Activity[I, O]{name: name, config: cfg, handler: handler}
	registerDeclaration(a)
	return a
}

func (w *Workflow[I, O]) Name() string {
	if w == nil {
		return ""
	}
	return w.name
}

func (w *Workflow[I, O]) Config() WorkflowConfig {
	if w == nil {
		return WorkflowConfig{}
	}
	return w.config
}

func (w *Workflow[I, O]) taskQueue(info onlavaruntime.TemporalRuntimeInfo) string {
	if w != nil && strings.TrimSpace(w.config.TaskQueue) != "" {
		return strings.TrimSpace(w.config.TaskQueue)
	}
	return defaultWorkerTaskQueue(info)
}

func (w *Workflow[I, O]) maxConcurrentActivityExecutions() int {
	return 0
}

func (w *Workflow[I, O]) register(worker temporalworker.Worker) {
	worker.RegisterWorkflowWithOptions(w.handler, workflow.RegisterOptions{
		Name:               w.name,
		VersioningBehavior: workflowVersioningBehavior(w.config.VersioningBehavior),
	})
}

func (w *Workflow[I, O]) declarationKey() string {
	if w == nil {
		return ""
	}
	return "workflow:" + w.name
}

func (a *Activity[I, O]) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

func (a *Activity[I, O]) Config() ActivityConfig {
	if a == nil {
		return ActivityConfig{}
	}
	return a.config
}

func (a *Activity[I, O]) taskQueue(info onlavaruntime.TemporalRuntimeInfo) string {
	if a != nil && strings.TrimSpace(a.config.TaskQueue) != "" {
		return strings.TrimSpace(a.config.TaskQueue)
	}
	return defaultWorkerTaskQueue(info)
}

func (a *Activity[I, O]) maxConcurrentActivityExecutions() int {
	if a == nil || a.config.MaxConcurrency <= 0 {
		return 0
	}
	return a.config.MaxConcurrency
}

func (a *Activity[I, O]) register(worker temporalworker.Worker) {
	worker.RegisterActivityWithOptions(a.handler, activity.RegisterOptions{Name: a.name})
}

func (a *Activity[I, O]) declarationKey() string {
	if a == nil {
		return ""
	}
	return "activity:" + a.name
}

func Start[I, O any](ctx context.Context, w *Workflow[I, O], input I, opts ...StartOption) (Run[O], error) {
	if w == nil {
		return Run[O]{}, errors.New("temporal: nil workflow")
	}
	client, info, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return Run[O]{}, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	start, err := startWorkflowOptions(info, w, opts...)
	if err != nil {
		return Run[O]{}, err
	}
	run, err := client.ExecuteWorkflow(ctx, start, w.name, input)
	if err != nil {
		return Run[O]{}, err
	}
	return Run[O]{run: run}, nil
}

func ExecuteActivity[I, O any](ctx workflow.Context, a *Activity[I, O], input I) ActivityFuture[O] {
	if a == nil {
		return ActivityFuture[O]{}
	}
	opts := workflow.ActivityOptions{
		TaskQueue:           strings.TrimSpace(a.config.TaskQueue),
		StartToCloseTimeout: a.config.StartToClose,
		RetryPolicy:         retryPolicy(a.config.RetryPolicy),
	}
	if opts.StartToCloseTimeout <= 0 {
		opts.StartToCloseTimeout = time.Minute
	}
	return ActivityFuture[O]{future: workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), a.name, input)}
}

func MethodActivity[I, SvcStruct any](handler func(s SvcStruct, ctx context.Context, input I) error) func(context.Context, I) (Void, error) {
	serviceKey := serviceKeyForType(reflect.TypeFor[SvcStruct]())
	return func(ctx context.Context, input I) (Void, error) {
		serviceAccessors.mu.RLock()
		accessor := serviceAccessors.items[serviceKey]
		serviceAccessors.mu.RUnlock()
		if accessor == nil {
			return Void{}, fmt.Errorf("temporal: no service accessor registered for %s", serviceKey)
		}
		svcAny, err := accessor()
		if err != nil {
			return Void{}, err
		}
		svc, ok := svcAny.(SvcStruct)
		if !ok {
			return Void{}, fmt.Errorf("temporal: service accessor returned %T, want %s", svcAny, serviceKey)
		}
		return Void{}, handler(svc, ctx, input)
	}
}

func MethodActivityResult[I, O, SvcStruct any](handler func(s SvcStruct, ctx context.Context, input I) (O, error)) func(context.Context, I) (O, error) {
	serviceKey := serviceKeyForType(reflect.TypeFor[SvcStruct]())
	return func(ctx context.Context, input I) (O, error) {
		var out O
		serviceAccessors.mu.RLock()
		accessor := serviceAccessors.items[serviceKey]
		serviceAccessors.mu.RUnlock()
		if accessor == nil {
			return out, fmt.Errorf("temporal: no service accessor registered for %s", serviceKey)
		}
		svcAny, err := accessor()
		if err != nil {
			return out, err
		}
		svc, ok := svcAny.(SvcStruct)
		if !ok {
			return out, fmt.Errorf("temporal: service accessor returned %T, want %s", svcAny, serviceKey)
		}
		return handler(svc, ctx, input)
	}
}

func RegisterServiceAccessorFor[T any](getter func() (any, error)) {
	if getter == nil {
		panic("temporal: service accessor getter must not be nil")
	}
	key := serviceKeyForType(reflect.TypeFor[T]())
	serviceAccessors.mu.Lock()
	defer serviceAccessors.mu.Unlock()
	serviceAccessors.items[key] = getter
}

func (r Run[O]) ID() string {
	if r.run == nil {
		return ""
	}
	return r.run.GetID()
}

func (r Run[O]) RunID() string {
	if r.run == nil {
		return ""
	}
	return r.run.GetRunID()
}

func (r Run[O]) Get(ctx context.Context) (O, error) {
	var out O
	if r.run == nil {
		return out, errors.New("temporal: nil workflow run")
	}
	if err := r.run.Get(ctx, &out); err != nil {
		return out, err
	}
	return out, nil
}

func GetWorkflow[O any](ctx context.Context, workflowID, runID string) (Run[O], error) {
	_ = ctx
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return Run[O]{}, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return Run[O]{}, errors.New("temporal: workflow ID must not be empty")
	}
	return Run[O]{run: client.GetWorkflow(context.Background(), workflowID, strings.TrimSpace(runID))}, nil
}

func (r Run[O]) Cancel(ctx context.Context) error {
	if r.run == nil {
		return errors.New("temporal: nil workflow run")
	}
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	return client.CancelWorkflow(ctx, r.ID(), r.RunID())
}

func (r Run[O]) Terminate(ctx context.Context, reason string, details ...any) error {
	if r.run == nil {
		return errors.New("temporal: nil workflow run")
	}
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	return client.TerminateWorkflow(ctx, r.ID(), r.RunID(), reason, details...)
}

func (r Run[O]) Signal(ctx context.Context, name string, arg any) error {
	if r.run == nil {
		return errors.New("temporal: nil workflow run")
	}
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	return client.SignalWorkflow(ctx, r.ID(), r.RunID(), name, arg)
}

func (r Run[O]) Query(ctx context.Context, queryType string, args ...any) (any, error) {
	if r.run == nil {
		return nil, errors.New("temporal: nil workflow run")
	}
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return nil, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	value, err := client.QueryWorkflow(ctx, r.ID(), r.RunID(), strings.TrimSpace(queryType), args...)
	if err != nil {
		return nil, err
	}
	var out any
	if err := value.Get(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r Run[O]) Update(ctx context.Context, updateName string, arg any) (any, error) {
	if r.run == nil {
		return nil, errors.New("temporal: nil workflow run")
	}
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return nil, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	handle, err := client.UpdateWorkflow(ctx, temporalclient.UpdateWorkflowOptions{
		WorkflowID:   r.ID(),
		RunID:        r.RunID(),
		UpdateName:   strings.TrimSpace(updateName),
		Args:         []interface{}{arg},
		WaitForStage: temporalclient.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return nil, err
	}
	var out any
	if err := handle.Get(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func Query[O any](ctx context.Context, workflowID, runID, queryType string, args ...any) (O, error) {
	var out O
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return out, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	value, err := client.QueryWorkflow(ctx, strings.TrimSpace(workflowID), strings.TrimSpace(runID), strings.TrimSpace(queryType), args...)
	if err != nil {
		return out, err
	}
	if err := value.Get(&out); err != nil {
		return out, err
	}
	return out, nil
}

func (f ActivityFuture[O]) Get(ctx workflow.Context) (O, error) {
	var out O
	if f.future == nil {
		return out, errors.New("temporal: nil activity future")
	}
	if err := f.future.Get(ctx, &out); err != nil {
		return out, err
	}
	return out, nil
}

func registerDeclaration(item declaration) {
	key := item.declarationKey()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.names[key]; exists {
		panic(fmt.Sprintf("temporal: duplicate declaration %q", key))
	}
	registry.names[key] = struct{}{}
	registry.items = append(registry.items, item)
}

func snapshotDeclarations() []declaration {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	items := make([]declaration, len(registry.items))
	copy(items, registry.items)
	return items
}

func startWorkerRuntime(ctx context.Context, cfg onlavaruntime.AppConfig) (func(context.Context) error, error) {
	_ = ctx
	items := snapshotDeclarations()
	if len(items) == 0 || strings.EqualFold(strings.TrimSpace(cfg.Role), "api") {
		return func(context.Context) error { return nil }, nil
	}
	client, info, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return nil, errors.New("temporal: declarations require temporal.enabled in .onlava.json")
	}

	byQueue := make(map[string][]declaration)
	for _, item := range items {
		queue := item.taskQueue(info)
		if strings.TrimSpace(queue) == "" {
			return nil, fmt.Errorf("temporal: declaration %q resolved to an empty task queue", item.declarationKey())
		}
		byQueue[queue] = append(byQueue[queue], item)
	}

	workers := make([]temporalworker.Worker, 0, len(byQueue))
	for queue, queueItems := range byQueue {
		worker := temporalworker.New(client, queue, temporalWorkerOptionsForQueue(info, cfg.Role, queue, queueItems))
		for _, item := range queueItems {
			item.register(worker)
		}
		if err := worker.Start(); err != nil {
			for _, started := range workers {
				started.Stop()
			}
			return nil, fmt.Errorf("temporal: start worker on %s: %w", queue, err)
		}
		workers = append(workers, worker)
	}
	if onlavaruntime.TemporalShouldAutoPromoteWorkers(info) {
		if err := onlavaruntime.EnsureTemporalWorkerDeploymentCurrentVersion(ctx, client, info); err != nil {
			for _, started := range workers {
				started.Stop()
			}
			return nil, err
		}
	} else {
		slog.Info("temporal worker deployment auto-promotion skipped outside local mode",
			"deployment", onlavaruntime.TemporalDeploymentName(info),
			"build_id", onlavaruntime.TemporalWorkerBuildID(info),
		)
	}
	return func(context.Context) error {
		for _, started := range workers {
			started.Stop()
		}
		return nil
	}, nil
}

func temporalWorkerOptionsForQueue(info onlavaruntime.TemporalRuntimeInfo, role, queue string, items []declaration) temporalworker.Options {
	opts := onlavaruntime.TemporalWorkerOptions(info, role, queue)
	concurrencyByActivity := make(map[int][]string)
	for _, item := range items {
		max := item.maxConcurrentActivityExecutions()
		if max <= 0 {
			continue
		}
		concurrencyByActivity[max] = append(concurrencyByActivity[max], item.declarationKey())
		if opts.MaxConcurrentActivityExecutionSize == 0 || max < opts.MaxConcurrentActivityExecutionSize {
			opts.MaxConcurrentActivityExecutionSize = max
		}
	}
	if len(concurrencyByActivity) > 1 {
		values := make([]int, 0, len(concurrencyByActivity))
		for value := range concurrencyByActivity {
			values = append(values, value)
		}
		sort.Ints(values)
		slog.Warn("temporal task queue has multiple activity MaxConcurrency values; worker limit uses the smallest value",
			"task_queue", queue,
			"max_concurrency_values", values,
			"effective_max_concurrency", opts.MaxConcurrentActivityExecutionSize,
		)
	}
	return opts
}

func WorkflowID(id string) StartOption {
	return func(opts *startOptions) {
		opts.id = strings.TrimSpace(id)
	}
}

func WithWorkflowID(id string) StartOption {
	return WorkflowID(id)
}

func WorkflowIDPrefix(prefix string) StartOption {
	return func(opts *startOptions) {
		opts.idPrefix = strings.TrimSpace(prefix)
	}
}

func WithWorkflowIDPrefix(prefix string) StartOption {
	return WorkflowIDPrefix(prefix)
}

func WithTaskQueue(queue string) StartOption {
	return func(opts *startOptions) {
		opts.taskQueue = strings.TrimSpace(queue)
	}
}

func WithMemo(memo map[string]any) StartOption {
	return func(opts *startOptions) {
		opts.memo = cloneAnyMap(memo)
	}
}

func WithSearchAttributes(attrs map[string]any) StartOption {
	return func(opts *startOptions) {
		opts.searchAttributes = cloneAnyMap(attrs)
	}
}

func WithExecutionTimeout(timeout time.Duration) StartOption {
	return func(opts *startOptions) {
		opts.workflowExecutionTimeout = timeout
	}
}

func WithRunTimeout(timeout time.Duration) StartOption {
	return func(opts *startOptions) {
		opts.workflowRunTimeout = timeout
	}
}

func WithTaskTimeout(timeout time.Duration) StartOption {
	return func(opts *startOptions) {
		opts.workflowTaskTimeout = timeout
	}
}

func WithPinnedBuildID(buildID string) StartOption {
	return func(opts *startOptions) {
		opts.pinnedBuildID = strings.TrimSpace(buildID)
	}
}

func WithWorkflowIDConflictPolicy(policy WorkflowIDConflictPolicy) StartOption {
	return func(opts *startOptions) {
		opts.workflowIDConflictPolicy = policy
	}
}

func WithWorkflowIDReusePolicy(policy WorkflowIDReusePolicy) StartOption {
	return func(opts *startOptions) {
		opts.workflowIDReusePolicy = policy
	}
}

func startWorkflowOptions[I, O any](info onlavaruntime.TemporalRuntimeInfo, w *Workflow[I, O], options ...StartOption) (temporalclient.StartWorkflowOptions, error) {
	start := startOptions{
		taskQueue:                w.taskQueue(info),
		workflowExecutionTimeout: w.config.WorkflowExecutionTimeout,
		workflowRunTimeout:       w.config.WorkflowRunTimeout,
		workflowTaskTimeout:      w.config.WorkflowTaskTimeout,
	}
	if w.config.WorkflowID != "" {
		start.id = strings.TrimSpace(w.config.WorkflowID)
	}
	if w.config.WorkflowIDPrefix != "" {
		start.idPrefix = strings.TrimSpace(w.config.WorkflowIDPrefix)
	}
	for _, option := range options {
		if option != nil {
			option(&start)
		}
	}
	if start.pinnedBuildID != "" {
		start.versioningOverride = &temporalclient.PinnedVersioningOverride{
			Version: temporalworker.WorkerDeploymentVersion{
				DeploymentName: onlavaruntime.TemporalDeploymentName(info),
				BuildID:        start.pinnedBuildID,
			},
		}
	}
	if start.taskQueue == "" {
		return temporalclient.StartWorkflowOptions{}, fmt.Errorf("temporal: workflow %s task queue must not be empty", w.name)
	}
	id := start.id
	if id == "" {
		prefix := start.idPrefix
		if prefix == "" {
			prefix = sanitizeName(w.name)
		}
		id = randomWorkflowID(prefix)
	}
	opts := temporalclient.StartWorkflowOptions{
		ID:                       id,
		TaskQueue:                start.taskQueue,
		WorkflowExecutionTimeout: start.workflowExecutionTimeout,
		WorkflowRunTimeout:       start.workflowRunTimeout,
		WorkflowTaskTimeout:      start.workflowTaskTimeout,
		Memo:                     start.memo,
		SearchAttributes:         start.searchAttributes,
		VersioningOverride:       start.versioningOverride,
		WorkflowIDConflictPolicy: enumspb.WorkflowIdConflictPolicy(start.workflowIDConflictPolicy),
		WorkflowIDReusePolicy:    enumspb.WorkflowIdReusePolicy(start.workflowIDReusePolicy),
	}
	return opts, nil
}

func randomWorkflowID(prefix string) string {
	if prefix = strings.Trim(strings.ToLower(prefix), ".-_/ "); prefix == "" {
		prefix = "workflow"
	}
	return "onlava." + sanitizeName(prefix) + "." + fmt.Sprintf("%d", time.Now().UTC().UnixNano()) + "." + randomHex(6)
}

func defaultWorkerTaskQueue(info onlavaruntime.TemporalRuntimeInfo) string {
	prefix := strings.TrimSpace(info.TaskQueuePrefix)
	if prefix == "" {
		prefix = "onlava"
	}
	return strings.TrimSuffix(prefix, ".") + ".worker.go"
}

func retryPolicy(policy RetryPolicy) *temporalerror.RetryPolicy {
	if retryPolicyIsZero(policy) {
		return nil
	}
	return &temporalerror.RetryPolicy{
		InitialInterval:        policy.InitialInterval,
		BackoffCoefficient:     policy.BackoffCoefficient,
		MaximumInterval:        policy.MaximumInterval,
		MaximumAttempts:        policy.MaximumAttempts,
		NonRetryableErrorTypes: append([]string(nil), policy.NonRetryableErrorTypes...),
	}
}

func retryPolicyIsZero(policy RetryPolicy) bool {
	return policy.InitialInterval == 0 &&
		policy.BackoffCoefficient == 0 &&
		policy.MaximumInterval == 0 &&
		policy.MaximumAttempts == 0 &&
		len(policy.NonRetryableErrorTypes) == 0
}

func validateActivityConfig(name string, cfg ActivityConfig) error {
	if strings.TrimSpace(cfg.TaskQueue) == "" {
		return fmt.Errorf("temporal: activity %s task queue must not be empty", name)
	}
	if cfg.StartToClose < 0 {
		return fmt.Errorf("temporal: activity %s StartToClose cannot be negative", name)
	}
	if cfg.MaxConcurrency < 0 {
		return fmt.Errorf("temporal: activity %s MaxConcurrency cannot be negative", name)
	}
	if err := validateRetryPolicy(name, cfg.RetryPolicy); err != nil {
		return err
	}
	return nil
}

func validateRetryPolicy(name string, policy RetryPolicy) error {
	if policy.InitialInterval < 0 {
		return fmt.Errorf("temporal: activity %s retry InitialInterval cannot be negative", name)
	}
	if policy.BackoffCoefficient < 0 {
		return fmt.Errorf("temporal: activity %s retry BackoffCoefficient cannot be negative", name)
	}
	if policy.MaximumInterval < 0 {
		return fmt.Errorf("temporal: activity %s retry MaximumInterval cannot be negative", name)
	}
	if policy.MaximumAttempts < 0 {
		return fmt.Errorf("temporal: activity %s retry MaximumAttempts cannot be negative", name)
	}
	if policy.InitialInterval > 0 && policy.MaximumInterval > 0 && policy.MaximumInterval < policy.InitialInterval {
		return fmt.Errorf("temporal: activity %s retry MaximumInterval cannot be less than InitialInterval", name)
	}
	if policy.BackoffCoefficient == 0 && (policy.InitialInterval != 0 || policy.MaximumInterval != 0 || policy.MaximumAttempts != 0) {
		return fmt.Errorf("temporal: activity %s retry BackoffCoefficient must be set when retry policy is configured", name)
	}
	return nil
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func serviceKeyForType(t reflect.Type) string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return ""
	}
	if pkgPath := t.PkgPath(); pkgPath != "" && t.Name() != "" {
		return pkgPath + "." + t.Name()
	}
	return t.String()
}

func workflowVersioningBehavior(behavior VersioningBehavior) workflow.VersioningBehavior {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(string(behavior)), "-", "_")) {
	case string(VersioningPinned):
		return workflow.VersioningBehaviorPinned
	case "auto", "autoupgrade", string(VersioningAutoUpgrade):
		return workflow.VersioningBehaviorAutoUpgrade
	default:
		return workflow.VersioningBehaviorUnspecified
	}
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "default"
	}
	var b strings.Builder
	lastDot := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDot = false
		case !lastDot:
			b.WriteByte('.')
			lastDot = true
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "default"
	}
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf)
}
