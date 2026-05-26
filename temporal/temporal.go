package temporal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	temporalclient "go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

type WorkflowContext = workflow.Context

type WorkflowConfig struct {
	TaskQueue                string
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

type WorkflowIdentity struct {
	id     string
	prefix string
}

type StartWorkflowOptions struct {
	TaskQueue                string
	Memo                     map[string]any
	SearchAttributes         SearchAttributes
	WorkflowExecutionTimeout time.Duration
	WorkflowRunTimeout       time.Duration
	WorkflowTaskTimeout      time.Duration
	PinnedBuildID            string
	WorkflowIDConflictPolicy WorkflowIDConflictPolicy
	WorkflowIDReusePolicy    WorkflowIDReusePolicy
}

type StartOption func(*StartWorkflowOptions)

type (
	SearchAttributes              = sdktemporal.SearchAttributes
	SearchAttributeUpdate         = sdktemporal.SearchAttributeUpdate
	SearchAttributeKeyString      = sdktemporal.SearchAttributeKeyString
	SearchAttributeKeyKeyword     = sdktemporal.SearchAttributeKeyKeyword
	SearchAttributeKeyBool        = sdktemporal.SearchAttributeKeyBool
	SearchAttributeKeyInt64       = sdktemporal.SearchAttributeKeyInt64
	SearchAttributeKeyFloat64     = sdktemporal.SearchAttributeKeyFloat64
	SearchAttributeKeyTime        = sdktemporal.SearchAttributeKeyTime
	SearchAttributeKeyKeywordList = sdktemporal.SearchAttributeKeyKeywordList
)

type WorkflowIDConflictPolicy string

const (
	WorkflowIDConflictDefault           WorkflowIDConflictPolicy = ""
	WorkflowIDConflictFail              WorkflowIDConflictPolicy = "fail"
	WorkflowIDConflictUseExisting       WorkflowIDConflictPolicy = "use_existing"
	WorkflowIDConflictTerminateExisting WorkflowIDConflictPolicy = "terminate_existing"
)

type WorkflowIDReusePolicy string

const (
	WorkflowIDReuseDefault              WorkflowIDReusePolicy = ""
	WorkflowIDReuseAllowDuplicate       WorkflowIDReusePolicy = "allow_duplicate"
	WorkflowIDReuseAllowDuplicateFailed WorkflowIDReusePolicy = "allow_duplicate_failed"
	WorkflowIDReuseRejectDuplicate      WorkflowIDReusePolicy = "reject_duplicate"
)

type RetryPolicy struct {
	InitialInterval        time.Duration
	BackoffCoefficient     float64
	MaximumInterval        time.Duration
	MaximumAttempts        int32
	NonRetryableErrorTypes []string
}

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
	run        temporalclient.WorkflowRun
	client     temporalclient.Client
	workflowID string
	runID      string
	err        error
}

type ActivityFuture[O any] struct {
	future workflow.Future
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
	_ = info
	if a != nil {
		return strings.TrimSpace(a.config.TaskQueue)
	}
	return ""
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

func Start[I, O any](ctx context.Context, w *Workflow[I, O], input I, identity WorkflowIdentity, opts ...StartOption) (Run[O], error) {
	if w == nil {
		return Run[O]{}, errors.New("temporal: nil workflow")
	}
	if identity.isZero() {
		return Run[O]{}, errors.New("temporal: workflow start requires WorkflowID or WorkflowIDPrefix")
	}
	startOpts := applyStartOptions(w.config, opts...)
	client, info, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return Run[O]{}, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	sdkOpts := temporalStartWorkflowOptions(w.name, identity, startOpts, w.taskQueue(info), info)
	run, err := client.ExecuteWorkflow(ctx, sdkOpts, w.name, input)
	if err != nil {
		return Run[O]{}, err
	}
	return Run[O]{run: run, client: client, workflowID: run.GetID(), runID: run.GetRunID()}, nil
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
	wrapped := MethodActivityResult[I, Void, SvcStruct](func(s SvcStruct, ctx context.Context, input I) (Void, error) {
		return Void{}, handler(s, ctx, input)
	})
	return wrapped
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

func WorkflowID(id string) WorkflowIdentity {
	id = strings.TrimSpace(id)
	if id == "" {
		panic("temporal: workflow id must not be empty")
	}
	return WorkflowIdentity{id: id}
}

func WorkflowIDPrefix(prefix string) WorkflowIdentity {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		panic("temporal: workflow id prefix must not be empty")
	}
	return WorkflowIdentity{prefix: prefix}
}

func WithTaskQueue(queue string) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.TaskQueue = strings.TrimSpace(queue)
	}
}

func WithMemo(memo map[string]any) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.Memo = cloneStringAnyMap(memo)
	}
}

func NewSearchAttributeKeyString(name string) SearchAttributeKeyString {
	return sdktemporal.NewSearchAttributeKeyString(name)
}

func NewSearchAttributeKeyKeyword(name string) SearchAttributeKeyKeyword {
	return sdktemporal.NewSearchAttributeKeyKeyword(name)
}

func NewSearchAttributeKeyBool(name string) SearchAttributeKeyBool {
	return sdktemporal.NewSearchAttributeKeyBool(name)
}

func NewSearchAttributeKeyInt64(name string) SearchAttributeKeyInt64 {
	return sdktemporal.NewSearchAttributeKeyInt64(name)
}

func NewSearchAttributeKeyFloat64(name string) SearchAttributeKeyFloat64 {
	return sdktemporal.NewSearchAttributeKeyFloat64(name)
}

func NewSearchAttributeKeyTime(name string) SearchAttributeKeyTime {
	return sdktemporal.NewSearchAttributeKeyTime(name)
}

func NewSearchAttributeKeyKeywordList(name string) SearchAttributeKeyKeywordList {
	return sdktemporal.NewSearchAttributeKeyKeywordList(name)
}

func NewSearchAttributes(attributes ...SearchAttributeUpdate) SearchAttributes {
	return sdktemporal.NewSearchAttributes(attributes...)
}

func WithSearchAttributes(attrs SearchAttributes) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.SearchAttributes = attrs
	}
}

func WithExecutionTimeout(d time.Duration) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.WorkflowExecutionTimeout = d
	}
}

func WithRunTimeout(d time.Duration) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.WorkflowRunTimeout = d
	}
}

func WithTaskTimeout(d time.Duration) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.WorkflowTaskTimeout = d
	}
}

func WithPinnedBuildID(buildID string) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.PinnedBuildID = strings.TrimSpace(buildID)
	}
}

func WithWorkflowIDConflictPolicy(policy WorkflowIDConflictPolicy) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.WorkflowIDConflictPolicy = policy
	}
}

func WithWorkflowIDReusePolicy(policy WorkflowIDReusePolicy) StartOption {
	return func(opts *StartWorkflowOptions) {
		opts.WorkflowIDReusePolicy = policy
	}
}

func GetWorkflow[O any](ctx context.Context, workflowID, runID string) Run[O] {
	client, _, ok := onlavaruntime.ActiveTemporalClient()
	if !ok || client == nil {
		return Run[O]{workflowID: workflowID, runID: runID, err: errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")}
	}
	workflowID = strings.TrimSpace(workflowID)
	runID = strings.TrimSpace(runID)
	if workflowID == "" {
		return Run[O]{client: client, workflowID: workflowID, runID: runID, err: errors.New("temporal: workflow id must not be empty")}
	}
	run := client.GetWorkflow(ctx, workflowID, runID)
	return Run[O]{run: run, client: client, workflowID: workflowID, runID: runID}
}

func (r Run[O]) ID() string {
	if r.run == nil {
		return r.workflowID
	}
	return r.run.GetID()
}

func (r Run[O]) RunID() string {
	if r.run == nil {
		return r.runID
	}
	return r.run.GetRunID()
}

func (r Run[O]) Get(ctx context.Context) (O, error) {
	var out O
	if r.err != nil {
		return out, r.err
	}
	if r.run == nil {
		return out, errors.New("temporal: nil workflow run")
	}
	if err := r.run.Get(ctx, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (r Run[O]) Cancel(ctx context.Context) error {
	if r.err != nil {
		return r.err
	}
	client := r.client
	if client == nil {
		client, _, _ = onlavaruntime.ActiveTemporalClient()
	}
	if client == nil {
		return errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	workflowID := r.ID()
	if strings.TrimSpace(workflowID) == "" {
		return errors.New("temporal: workflow id must not be empty")
	}
	return client.CancelWorkflow(ctx, workflowID, r.RunID())
}

func (r Run[O]) Terminate(ctx context.Context, reason string) error {
	if r.err != nil {
		return r.err
	}
	client := r.client
	if client == nil {
		client, _, _ = onlavaruntime.ActiveTemporalClient()
	}
	if client == nil {
		return errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	workflowID := r.ID()
	if strings.TrimSpace(workflowID) == "" {
		return errors.New("temporal: workflow id must not be empty")
	}
	return client.TerminateWorkflow(ctx, workflowID, r.RunID(), reason)
}

func Signal[O, I any](ctx context.Context, run Run[O], name string, input I) error {
	client, err := temporalClientForRun(run)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("temporal: signal name must not be empty")
	}
	return client.SignalWorkflow(ctx, run.ID(), run.RunID(), name, input)
}

func Query[O, R any](ctx context.Context, run Run[R], name string) (O, error) {
	var out O
	client, err := temporalClientForRun(run)
	if err != nil {
		return out, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return out, errors.New("temporal: query name must not be empty")
	}
	value, err := client.QueryWorkflow(ctx, run.ID(), run.RunID(), name)
	if err != nil {
		return out, err
	}
	if err := value.Get(&out); err != nil {
		return out, err
	}
	return out, nil
}

func Update[I, O, R any](ctx context.Context, run Run[R], name string, input I) (O, error) {
	var out O
	client, err := temporalClientForRun(run)
	if err != nil {
		return out, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return out, errors.New("temporal: update name must not be empty")
	}
	handle, err := client.UpdateWorkflow(ctx, temporalclient.UpdateWorkflowOptions{
		WorkflowID:   run.ID(),
		RunID:        run.RunID(),
		UpdateName:   name,
		Args:         []interface{}{input},
		WaitForStage: temporalclient.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return out, err
	}
	if err := handle.Get(ctx, &out); err != nil {
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
	byQueue, err := filterDeclarationsBySelectedTaskQueues(byQueue, selectedTemporalTaskQueuesFromEnv())
	if err != nil {
		return nil, err
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
	if onlavaruntime.ShouldAutoPromoteTemporalWorkerDeployment(info) {
		if err := onlavaruntime.EnsureTemporalWorkerDeploymentCurrentVersion(ctx, client, info); err != nil {
			for _, started := range workers {
				started.Stop()
			}
			return nil, err
		}
	}
	return func(context.Context) error {
		for _, worker := range workers {
			worker.Stop()
		}
		return nil
	}, nil
}

func selectedTemporalTaskQueuesFromEnv() []string {
	return splitTemporalTaskQueueList(os.Getenv("ONLAVA_TEMPORAL_TASK_QUEUE"))
}

func splitTemporalTaskQueueList(value string) []string {
	parts := strings.Split(value, ",")
	queues := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		queue := strings.TrimSpace(part)
		if queue == "" {
			continue
		}
		if _, ok := seen[queue]; ok {
			continue
		}
		seen[queue] = struct{}{}
		queues = append(queues, queue)
	}
	return queues
}

func filterDeclarationsBySelectedTaskQueues(byQueue map[string][]declaration, selected []string) (map[string][]declaration, error) {
	selected = normalizeSelectedTaskQueues(selected)
	if len(selected) == 0 {
		return byQueue, nil
	}
	filtered := make(map[string][]declaration, len(selected))
	var missing []string
	for _, queue := range selected {
		items, ok := byQueue[queue]
		if !ok {
			missing = append(missing, queue)
			continue
		}
		filtered[queue] = items
	}
	if len(missing) > 0 {
		declared := make([]string, 0, len(byQueue))
		for queue := range byQueue {
			declared = append(declared, queue)
		}
		sort.Strings(declared)
		return nil, fmt.Errorf("temporal: selected task queue(s) not declared: %s; declared task queues: %s", strings.Join(missing, ", "), strings.Join(declared, ", "))
	}
	return filtered, nil
}

func normalizeSelectedTaskQueues(selected []string) []string {
	queues := make([]string, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, queue := range selected {
		queue = strings.TrimSpace(queue)
		if queue == "" {
			continue
		}
		if _, ok := seen[queue]; ok {
			continue
		}
		seen[queue] = struct{}{}
		queues = append(queues, queue)
	}
	return queues
}

func temporalWorkerOptionsForQueue(info onlavaruntime.TemporalRuntimeInfo, role, queue string, items []declaration) temporalworker.Options {
	opts := onlavaruntime.TemporalWorkerOptions(info, role, queue)
	for _, item := range items {
		max := item.maxConcurrentActivityExecutions()
		if max <= 0 {
			continue
		}
		if opts.MaxConcurrentActivityExecutionSize == 0 || max < opts.MaxConcurrentActivityExecutionSize {
			opts.MaxConcurrentActivityExecutionSize = max
		}
	}
	return opts
}

func applyStartOptions(cfg WorkflowConfig, options ...StartOption) StartWorkflowOptions {
	opts := StartWorkflowOptions{
		TaskQueue:                strings.TrimSpace(cfg.TaskQueue),
		WorkflowExecutionTimeout: cfg.WorkflowExecutionTimeout,
		WorkflowRunTimeout:       cfg.WorkflowRunTimeout,
		WorkflowTaskTimeout:      cfg.WorkflowTaskTimeout,
	}
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	opts.Memo = cloneStringAnyMap(opts.Memo)
	return opts
}

func temporalStartWorkflowOptions(workflowName string, identity WorkflowIdentity, opts StartWorkflowOptions, defaultTaskQueue string, info onlavaruntime.TemporalRuntimeInfo) temporalclient.StartWorkflowOptions {
	taskQueue := strings.TrimSpace(opts.TaskQueue)
	if taskQueue == "" {
		taskQueue = defaultTaskQueue
	}
	start := temporalclient.StartWorkflowOptions{
		ID:                       workflowIDFromIdentity(workflowName, identity),
		TaskQueue:                taskQueue,
		Memo:                     cloneStringAnyMap(opts.Memo),
		TypedSearchAttributes:    opts.SearchAttributes,
		WorkflowExecutionTimeout: opts.WorkflowExecutionTimeout,
		WorkflowRunTimeout:       opts.WorkflowRunTimeout,
		WorkflowTaskTimeout:      opts.WorkflowTaskTimeout,
		WorkflowIDConflictPolicy: workflowIDConflictPolicy(opts.WorkflowIDConflictPolicy),
		WorkflowIDReusePolicy:    workflowIDReusePolicy(opts.WorkflowIDReusePolicy),
	}
	if buildID := strings.TrimSpace(opts.PinnedBuildID); buildID != "" {
		start.VersioningOverride = &temporalclient.PinnedVersioningOverride{
			Version: temporalworker.WorkerDeploymentVersion{
				DeploymentName: onlavaruntime.TemporalDeploymentName(info),
				BuildID:        buildID,
			},
		}
	}
	return start
}

func workflowIDFromIdentity(workflowName string, identity WorkflowIdentity) string {
	if id := strings.TrimSpace(identity.id); id != "" {
		return id
	}
	prefix := strings.TrimSpace(identity.prefix)
	if prefix == "" {
		prefix = sanitizeName(workflowName)
	}
	return randomWorkflowID(prefix)
}

func (identity WorkflowIdentity) isZero() bool {
	return strings.TrimSpace(identity.id) == "" && strings.TrimSpace(identity.prefix) == ""
}

func workflowIDConflictPolicy(policy WorkflowIDConflictPolicy) enumspb.WorkflowIdConflictPolicy {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(string(policy)), "-", "_")) {
	case string(WorkflowIDConflictFail):
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL
	case string(WorkflowIDConflictUseExisting):
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING
	case string(WorkflowIDConflictTerminateExisting):
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING
	default:
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_UNSPECIFIED
	}
}

func workflowIDReusePolicy(policy WorkflowIDReusePolicy) enumspb.WorkflowIdReusePolicy {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(string(policy)), "-", "_")) {
	case string(WorkflowIDReuseAllowDuplicate):
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	case string(WorkflowIDReuseAllowDuplicateFailed), "allow_duplicate_failed_only":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
	case string(WorkflowIDReuseRejectDuplicate):
		return enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	default:
		return enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED
	}
}

func temporalClientForRun[O any](run Run[O]) (temporalclient.Client, error) {
	if run.err != nil {
		return nil, run.err
	}
	client := run.client
	if client == nil {
		client, _, _ = onlavaruntime.ActiveTemporalClient()
	}
	if client == nil {
		return nil, errors.New("temporal: runtime is not started; set temporal.enabled in .onlava.json")
	}
	if strings.TrimSpace(run.ID()) == "" {
		return nil, errors.New("temporal: workflow id must not be empty")
	}
	return client, nil
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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

func retryPolicy(policy RetryPolicy) *sdktemporal.RetryPolicy {
	if retryPolicyIsZero(policy) {
		return nil
	}
	return &sdktemporal.RetryPolicy{
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
