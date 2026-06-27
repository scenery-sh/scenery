package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/durable/store"
	"scenery.sh/internal/envpolicy"
)

type durableContextKey string

const (
	durableContextStore durableContextKey = "durable.store"
	durableContextJobID durableContextKey = "durable.job_id"
)

type DurableStartRequest struct {
	Service   string
	TaskName  string
	ID        string
	DedupeKey string
	Input     any
}

type DurableRun struct {
	ID        string
	Service   string
	TaskName  string
	State     string
	DedupeKey string
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
	handlers := make(map[string]map[string]func(context.Context, []byte) ([]byte, error))
	for _, task := range tasks {
		service, err := store.NormalizeServiceName(task.Service)
		if err != nil {
			return nil, err
		}
		byService[service] = append(byService[service], durableStoreTaskDeclaration(task))
		if handlers[service] == nil {
			handlers[service] = make(map[string]func(context.Context, []byte) ([]byte, error))
		}
		handlers[service][task.Name] = task.Handler
	}
	if remoteCfg := durableRemoteWorkerConfigFromEnv(); remoteCfg.Endpoint != "" {
		if remoteCfg.Token == "" {
			return nil, fmt.Errorf("runtime: %s is required when %s is set", envDurableToken, envDurableEndpoint)
		}
		return startDurableRemoteWorkers(ctx, handlers, remoteCfg), nil
	}
	stateRoot, err := durableStateRoot()
	if err != nil {
		return nil, err
	}
	var opened []*store.Store
	for service, declarations := range byService {
		path, err := store.DurableDBPath(stateRoot, service)
		if err != nil {
			closeDurableStores(opened)
			return nil, err
		}
		db, err := store.Open(ctx, service, path, store.Options{})
		if err != nil {
			closeDurableStores(opened)
			return nil, err
		}
		if err := db.ReconcileTasks(ctx, declarations); err != nil {
			_ = db.Close()
			closeDurableStores(opened)
			return nil, err
		}
		opened = append(opened, db)
	}
	setActiveDurableStores(opened)
	stopWorkers := startDurableLocalWorkers(ctx, opened, handlers, cfg.Role)
	stopSchedules := startDurableScheduleLoop(ctx, opened, cfg.Role)
	return func(stopCtx context.Context) error {
		workerErr := stopWorkers(stopCtx)
		scheduleErr := stopSchedules(stopCtx)
		clearActiveDurableStores(opened)
		return errors.Join(workerErr, scheduleErr, closeDurableStores(opened))
	}, nil
}

func StartDurableTask(ctx context.Context, req DurableStartRequest) (DurableRun, error) {
	service, err := store.NormalizeServiceName(req.Service)
	if err != nil {
		return DurableRun{}, err
	}
	taskName := strings.TrimSpace(req.TaskName)
	if taskName == "" {
		return DurableRun{}, errors.New("runtime: durable task name is required")
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
	job, err := db.Start(ctx, store.StartRequest{
		ID:               id,
		TaskName:         taskName,
		TaskVersion:      1,
		DedupeKey:        strings.TrimSpace(req.DedupeKey),
		InputCodec:       "json",
		InputBlob:        input,
		RequirementsJSON: "{}",
		LabelsJSON:       "{}",
		MemoJSON:         "{}",
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

func startDurableLocalWorkers(parent context.Context, stores []*store.Store, handlers map[string]map[string]func(context.Context, []byte) ([]byte, error), role string) func(context.Context) error {
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
		go func(db *store.Store, serviceHandlers map[string]func(context.Context, []byte) ([]byte, error)) {
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

func runDurableLocalWorker(ctx context.Context, db *store.Store, workerID string, handlers map[string]func(context.Context, []byte) ([]byte, error)) {
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
		handler := handlers[job.TaskName]
		if handler == nil {
			_ = db.FailJob(ctx, job.ID, []byte("missing durable task handler"))
			continue
		}
		jobCtx := context.WithValue(ctx, durableContextStore, db)
		jobCtx = context.WithValue(jobCtx, durableContextJobID, job.ID)
		result, err := handler(jobCtx, job.InputBlob)
		if err != nil {
			_ = db.FailJob(ctx, job.ID, []byte(err.Error()))
			continue
		}
		if err := db.CompleteJob(ctx, job.ID, result); err != nil {
			sleepDurableWorker(ctx)
		}
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

func durableStateRoot() (string, error) {
	appRoot := strings.TrimSpace(envpolicy.Get("SCENERY_APP_ROOT"))
	if appRoot == "" {
		return "", errors.New("runtime: durable tasks require SCENERY_APP_ROOT")
	}
	return filepath.Join(appRoot, ".scenery", "state"), nil
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
		Name:             task.Name,
		Version:          1,
		HandlerRef:       handlerRef,
		InputCodec:       "json",
		ResultCodec:      "json",
		DefaultTimeoutMS: durationMS(task.DefaultTimeout),
		DefaultLeaseMS:   durationMS(task.DefaultLease),
		MaxAttempts:      task.MaxAttempts,
		RetryInitialMS:   durationMS(task.RetryInitial),
		RetryMaxMS:       durationMS(task.RetryMax),
		RetryBackoff:     task.RetryBackoff,
		RetryJitter:      task.RetryJitter,
		RequirementsJSON: normalizedRequirementsJSON(task.RequirementsJSON),
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
