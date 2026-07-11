package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/durable/store"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

type durableContextKey string

const (
	durableContextStore durableContextKey = "durable.store"
	durableContextJobID durableContextKey = "durable.job_id"
)

type DurableStartRequest struct {
	Service        string
	TaskName       string
	ID             string
	DedupeKey      string
	ConcurrencyKey string
	Input          any
}

type DurableRun struct {
	ID        string
	Service   string
	TaskName  string
	State     string
	DedupeKey string
}

type DurableExecutionFailure struct {
	Service  string
	ID       string
	State    string
	TaskName string
}

type durableRegisteredHandler struct {
	handler func(context.Context, []byte) ([]byte, error)
	timeout time.Duration
}

type durableInvocationMetadata struct {
	Principal     string `json:"principal,omitempty"`
	TenantID      string `json:"tenant_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	CallerBinding string `json:"caller_binding,omitempty"`
	Deployment    string `json:"deployment,omitempty"`
	Locale        string `json:"locale,omitempty"`
}

func (failure *DurableExecutionFailure) Error() string {
	if failure == nil {
		return "runtime: durable execution failed"
	}
	return fmt.Sprintf("runtime: durable execution %s/%s reached %s", failure.Service, failure.ID, failure.State)
}

var activeDurableStores = struct {
	mu     sync.RWMutex
	stores map[string]*store.Store
}{
	stores: make(map[string]*store.Store),
}

func startDurableRuntime(ctx context.Context, cfg AppConfig) (func(context.Context) error, error) {
	_ = cfg
	tasks := listDurableTasks()
	if len(tasks) == 0 {
		return func(context.Context) error { return nil }, nil
	}
	byService := make(map[string][]store.TaskDeclaration)
	handlers := make(map[string]map[string]durableRegisteredHandler)
	for _, task := range tasks {
		service, err := store.NormalizeServiceName(task.Service)
		if err != nil {
			return nil, err
		}
		byService[service] = append(byService[service], durableStoreTaskDeclaration(task))
		if handlers[service] == nil {
			handlers[service] = make(map[string]durableRegisteredHandler)
		}
		handlers[service][task.Name] = durableRegisteredHandler{handler: task.Handler, timeout: task.DefaultTimeout}
	}
	if remoteCfg := durableRemoteWorkerConfigFromEnv(); remoteCfg.Endpoint != "" {
		if remoteCfg.Token == "" {
			return nil, fmt.Errorf("runtime: %s is required when %s is set", envDurableToken, envDurableEndpoint)
		}
		return startDurableRemoteWorkers(ctx, handlers, remoteCfg), nil
	}
	databaseURL, err := durableDatabaseURL()
	if err != nil {
		return nil, err
	}
	var opened []*store.Store
	var base *store.Store
	for service, declarations := range byService {
		var db *store.Store
		if base == nil {
			db, err = store.Open(ctx, service, databaseURL, store.Options{})
			base = db
		} else {
			db, err = base.ForService(service)
		}
		if err != nil {
			if base != nil {
				_ = base.Close()
			}
			closeDurableStores(opened)
			return nil, err
		}
		if err := db.ReconcileTasks(ctx, declarations); err != nil {
			if base != nil {
				_ = base.Close()
			}
			closeDurableStores(opened)
			return nil, err
		}
		opened = append(opened, db)
	}
	setActiveDurableStores(opened)
	stopWorkers := startDurableLocalWorkers(ctx, opened, handlers, cfg.Role)
	stopSchedules := startDurableScheduleLoop(ctx, opened, cfg.Role)
	stopRetention := startDurableRetentionLoop(ctx, opened)
	return func(stopCtx context.Context) error {
		workerErr := stopWorkers(stopCtx)
		scheduleErr := stopSchedules(stopCtx)
		retentionErr := stopRetention(stopCtx)
		clearActiveDurableStores(opened)
		return errors.Join(workerErr, scheduleErr, retentionErr, closeDurableStores(opened))
	}, nil
}

func startDurableRetentionLoop(parent context.Context, stores []*store.Store) func(context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		purge := func() {
			for _, db := range stores {
				_, _ = db.PurgeExpiredJobs(ctx)
			}
		}
		purge()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				purge()
			}
		}
	}()
	return func(stopCtx context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
	}
}

func StartDurableTask(ctx context.Context, req DurableStartRequest) (DurableRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	service, err := store.NormalizeServiceName(req.Service)
	if err != nil {
		return DurableRun{}, err
	}
	taskName := strings.TrimSpace(req.TaskName)
	if taskName == "" {
		return DurableRun{}, errors.New("runtime: durable task name is required")
	}
	global.mu.RLock()
	task := global.durableTasks[service+":"+taskName]
	global.mu.RUnlock()
	if task == nil {
		return DurableRun{}, fmt.Errorf("runtime: durable task %s:%s is not registered", service, taskName)
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id, err = newDurableJobID()
		if err != nil {
			return DurableRun{}, err
		}
	}
	input, err := json.Marshal(req.Input)
	if err != nil {
		return DurableRun{}, fmt.Errorf("runtime: marshal durable task input: %w", err)
	}

	activeDurableStores.mu.RLock()
	db := activeDurableStores.stores[service]
	activeDurableStores.mu.RUnlock()
	if db == nil {
		return DurableRun{}, fmt.Errorf("runtime: durable service %q is not active", service)
	}
	invocationJSON, err := durableInvocationMetadataJSON(ctx)
	if err != nil {
		return DurableRun{}, err
	}
	job, err := db.Start(ctx, store.StartRequest{
		ID:               id,
		TaskName:         taskName,
		TaskVersion:      task.Version,
		DedupeKey:        strings.TrimSpace(req.DedupeKey),
		ConcurrencyKey:   strings.TrimSpace(req.ConcurrencyKey),
		InputCodec:       "json",
		InputBlob:        input,
		RequirementsJSON: "{}",
		LabelsJSON:       "{}",
		MemoJSON:         invocationJSON,
		CreatedBy:        durableCreatedBy(),
	})
	if err != nil {
		return DurableRun{}, err
	}
	return DurableRun{
		ID:        job.ID,
		Service:   service,
		TaskName:  job.TaskName,
		State:     job.State,
		DedupeKey: job.DedupeKey,
	}, nil
}

func WaitDurableTask(ctx context.Context, run DurableRun) ([]byte, error) {
	service, err := store.NormalizeServiceName(run.Service)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.ID) == "" {
		return nil, errors.New("runtime: durable execution id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	activeDurableStores.mu.RLock()
	db := activeDurableStores.stores[service]
	activeDurableStores.mu.RUnlock()
	if db == nil {
		return nil, fmt.Errorf("runtime: durable service %q is not active", service)
	}
	for {
		job, found, err := db.GetJob(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("runtime: durable execution %s/%s was not found", service, run.ID)
		}
		switch job.State {
		case "succeeded":
			if job.ResultCodec != "json" || len(job.ResultBlob) == 0 {
				return nil, fmt.Errorf("runtime: durable execution %s/%s has no JSON result", service, run.ID)
			}
			return append([]byte(nil), job.ResultBlob...), nil
		case "failed", "canceled":
			return nil, &DurableExecutionFailure{Service: service, ID: run.ID, State: job.State, TaskName: job.TaskName}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func setActiveDurableStores(stores []*store.Store) {
	activeDurableStores.mu.Lock()
	defer activeDurableStores.mu.Unlock()
	activeDurableStores.stores = make(map[string]*store.Store, len(stores))
	for _, db := range stores {
		activeDurableStores.stores[db.Service] = db
	}
}

func clearActiveDurableStores(stores []*store.Store) {
	activeDurableStores.mu.Lock()
	defer activeDurableStores.mu.Unlock()
	for _, db := range stores {
		if activeDurableStores.stores[db.Service] == db {
			delete(activeDurableStores.stores, db.Service)
		}
	}
}

func durableInvocationMetadataJSON(ctx context.Context) (string, error) {
	invocation, ok := runtimeapi.InvocationFromContext(ctx)
	if !ok {
		return "{}", nil
	}
	metadata := durableInvocationMetadata{
		Principal: invocation.Principal(), TenantID: invocation.TenantID(), TraceID: invocation.TraceID(),
		CallerBinding: invocation.CallerBinding(), Deployment: invocation.Deployment(), Locale: invocation.Locale(),
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("runtime: encode durable invocation metadata: %w", err)
	}
	return string(encoded), nil
}

func durableInvocationMetadataFromJSON(value string) *durableInvocationMetadata {
	value = strings.TrimSpace(value)
	if value == "" || value == "{}" {
		return nil
	}
	var metadata durableInvocationMetadata
	if err := json.Unmarshal([]byte(value), &metadata); err != nil {
		return nil
	}
	return &metadata
}

func enterDurableInvocation(ctx context.Context, service, taskName, executionID string, timeout time.Duration, metadata *durableInvocationMetadata) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now().UTC()
	state := &requestState{
		started: started,
		request: shared.Request{
			Type: shared.DurableCall, Started: started, InvocationID: executionID,
			ExecutionID: executionID, Service: service, Endpoint: taskName, Method: "DURABLE",
			Path: taskName, Headers: make(map[string][]string),
		},
		logsEnabled: true, traceEnabled: true,
	}
	if timeout > 0 {
		state.request.Deadline = started.Add(timeout)
	}
	if metadata != nil {
		state.auth = AuthInfo{UID: metadata.Principal, Data: map[string]any{"tenant_id": metadata.TenantID}}
		state.request.TraceID, state.request.CallerBinding = metadata.TraceID, metadata.CallerBinding
		state.request.Deployment, state.request.Locale = metadata.Deployment, metadata.Locale
	}
	ctx = withState(ctx, state)
	ctx = withRuntimeInvocation(ctx, state)
	return ctx, enterState(state)
}

func startDurableLocalWorkers(parent context.Context, stores []*store.Store, handlers map[string]map[string]durableRegisteredHandler, role string) func(context.Context) error {
	if strings.EqualFold(strings.TrimSpace(role), "api") {
		return func(context.Context) error { return nil }
	}
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	workerID := fmt.Sprintf("local-%d", os.Getpid())
	for _, db := range stores {
		serviceHandlers := handlers[db.Service]
		if len(serviceHandlers) == 0 {
			continue
		}
		wg.Add(1)
		go func(db *store.Store, serviceHandlers map[string]durableRegisteredHandler) {
			defer wg.Done()
			runDurableLocalWorker(ctx, db, workerID, serviceHandlers)
		}(db, serviceHandlers)
	}
	return func(stopCtx context.Context) error {
		cancel()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
	}
}

func startDurableScheduleLoop(parent context.Context, stores []*store.Store, role string) func(context.Context) error {
	if strings.EqualFold(strings.TrimSpace(role), "worker") {
		return func(context.Context) error { return nil }
	}
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	for _, db := range stores {
		wg.Add(1)
		go func(db *store.Store) {
			defer wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if _, err := db.RunDueSchedules(ctx, time.Now()); err != nil {
					sleepDurableWorker(ctx)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(db)
	}
	return func(stopCtx context.Context) error {
		cancel()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
	}
}

func runDurableLocalWorker(ctx context.Context, db *store.Store, workerID string, handlers map[string]durableRegisteredHandler) {
	for {
		if ctx.Err() != nil {
			return
		}
		leaseID, err := newDurableID("lease_")
		if err != nil {
			sleepDurableWorker(ctx)
			continue
		}
		job, ok, err := db.LeaseReadyJob(ctx, workerID, leaseID)
		if err != nil {
			sleepDurableWorker(ctx)
			continue
		}
		if !ok {
			sleepDurableWorker(ctx)
			continue
		}
		handler, exists := handlers[job.TaskName]
		if !exists || handler.handler == nil {
			_ = db.FailLeasedJob(ctx, job.ID, workerID, leaseID, []byte("missing durable task handler"))
			continue
		}
		jobCtx := context.WithValue(ctx, durableContextStore, db)
		jobCtx = context.WithValue(jobCtx, durableContextJobID, job.ID)
		jobCtx, restore := enterDurableInvocation(jobCtx, db.Service, job.TaskName, job.ID, time.Duration(job.TimeoutMS)*time.Millisecond, durableInvocationMetadataFromJSON(job.MemoJSON))
		stopHeartbeat := startDurableHeartbeat(jobCtx, time.Duration(job.LeaseMS)*time.Millisecond, func(heartbeatCtx context.Context) error {
			return db.HeartbeatJob(heartbeatCtx, job.ID, workerID, leaseID)
		})
		result, err := runDurableTaskHandler(jobCtx, handler.timeout, handler.handler, job.InputBlob)
		stopHeartbeat()
		restore()
		if err != nil {
			message := "durable task failed"
			if errors.Is(err, context.DeadlineExceeded) {
				message = "durable task timed out"
			}
			_ = db.FailLeasedJob(ctx, job.ID, workerID, leaseID, []byte(message))
			continue
		}
		if err := db.CompleteLeasedJob(ctx, job.ID, workerID, leaseID, result); err != nil {
			sleepDurableWorker(ctx)
		}
	}
}

func runDurableTaskHandler(ctx context.Context, timeout time.Duration, handler func(context.Context, []byte) ([]byte, error), input []byte) (result []byte, err error) {
	if handler == nil {
		return nil, errors.New("runtime: durable task handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	handlerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("runtime: durable task panic: %v", recovered)
		}
	}()
	result, err = handler(handlerCtx, append([]byte(nil), input...))
	if handlerCtx.Err() != nil {
		return nil, handlerCtx.Err()
	}
	return result, err
}

func startDurableHeartbeat(ctx context.Context, lease time.Duration, heartbeat func(context.Context) error) func() {
	if heartbeat == nil {
		return func() {}
	}
	interval := lease / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := heartbeat(heartbeatCtx); err != nil {
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func sleepDurableWorker(ctx context.Context) {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func durableDatabaseURL() (string, error) {
	if dsn := strings.TrimSpace(envpolicy.Get("DATABASE_URL")); dsn != "" {
		return dsn, nil
	}
	registry, err := postgresdb.DecodeRegistry(envpolicy.Get(postgresdb.RegistryEnv))
	if err == nil && strings.TrimSpace(registry.URL) != "" {
		return registry.URL, nil
	}
	return "", errors.New("runtime: durable tasks require DATABASE_URL")
}

func durableStoreTaskDeclaration(task *DurableTask) store.TaskDeclaration {
	if task == nil {
		return store.TaskDeclaration{}
	}
	handlerRef := strings.TrimSpace(task.HandlerRef)
	if handlerRef == "" {
		handlerRef = task.Name
	}
	return store.TaskDeclaration{
		Name:                     task.Name,
		Version:                  task.Version,
		HandlerRef:               handlerRef,
		InputCodec:               "json",
		ResultCodec:              "json",
		DefaultTimeoutMS:         durationMS(task.DefaultTimeout),
		DefaultLeaseMS:           durationMS(task.DefaultLease),
		MaxAttempts:              task.MaxAttempts,
		RetryInitialMS:           durationMS(task.RetryInitial),
		RetryMaxMS:               durationMS(task.RetryMax),
		RetryBackoff:             task.RetryBackoff,
		RetryJitter:              task.RetryJitter,
		SuccessRetentionMS:       durationMS64(task.SuccessRetention),
		FailureRetentionMS:       durationMS64(task.FailureRetention),
		MaxConcurrency:           task.MaxConcurrency,
		DeduplicationRetentionMS: durationMS64(task.DeduplicationRetention),
		DeduplicationConflict:    task.DeduplicationConflict,
		RequirementsJSON:         normalizedRequirementsJSON(task.RequirementsJSON),
	}
}

func DurableSignal(ctx context.Context, service, jobID, name, dedupeKey string, payload []byte) error {
	service, err := store.NormalizeServiceName(service)
	if err != nil {
		return err
	}
	activeDurableStores.mu.RLock()
	db := activeDurableStores.stores[service]
	activeDurableStores.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("runtime: durable service %q is not active", service)
	}
	return db.SignalJob(ctx, jobID, name, dedupeKey, payload)
}

func DurableSchedule(ctx context.Context, service, taskName, id string, every time.Duration, input []byte) error {
	service, err := store.NormalizeServiceName(service)
	if err != nil {
		return err
	}
	activeDurableStores.mu.RLock()
	db := activeDurableStores.stores[service]
	activeDurableStores.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("runtime: durable service %q is not active", service)
	}
	return db.UpsertSchedule(ctx, id, taskName, every, input)
}

func DurableStep(ctx context.Context, key string, run func(context.Context) ([]byte, error)) ([]byte, error) {
	if run == nil {
		return nil, errors.New("runtime: durable step function is required")
	}
	db, _ := ctx.Value(durableContextStore).(*store.Store)
	jobID, _ := ctx.Value(durableContextJobID).(string)
	if db == nil || strings.TrimSpace(jobID) == "" {
		return run(ctx)
	}
	if step, ok, err := db.GetStep(ctx, jobID, key); err != nil {
		return nil, err
	} else if ok && step.State == "succeeded" {
		return step.ResultBlob, nil
	}
	result, err := run(ctx)
	if err != nil {
		_ = db.SaveStep(ctx, jobID, key, "failed", "json", nil, []byte(err.Error()))
		return nil, err
	}
	if err := db.SaveStep(ctx, jobID, key, "succeeded", "json", result, nil); err != nil {
		return nil, err
	}
	return result, nil
}

func durationMS(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d / time.Millisecond)
}

func durationMS64(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Millisecond)
}

func newDurableJobID() (string, error) {
	return newDurableID("job_")
}

func newDurableID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("runtime: generate durable id: %w", err)
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

func durableCreatedBy() string {
	meta := Meta()
	for _, value := range []string{meta.RuntimeAppID, meta.AppID, meta.BaseAppID} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "app"
}

func normalizedRequirementsJSON(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	if json.Valid([]byte(value)) {
		return value
	}
	return "{}"
}

func closeDurableStores(stores []*store.Store) error {
	var errs []error
	for _, db := range stores {
		if err := db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("runtime: close durable stores: %w", errors.Join(errs...))
	}
	return nil
}
