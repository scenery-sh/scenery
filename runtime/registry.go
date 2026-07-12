package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

type Access = runtimeapi.Access

const (
	Public  = runtimeapi.Public
	Auth    = runtimeapi.Auth
	Private = runtimeapi.Private
)

type ParamKind = runtimeapi.ParamKind

const (
	ParamString = runtimeapi.ParamString
	ParamBool   = runtimeapi.ParamBool
	ParamInt    = runtimeapi.ParamInt
	ParamInt8   = runtimeapi.ParamInt8
	ParamInt16  = runtimeapi.ParamInt16
	ParamInt32  = runtimeapi.ParamInt32
	ParamInt64  = runtimeapi.ParamInt64
	ParamUint   = runtimeapi.ParamUint
	ParamUint8  = runtimeapi.ParamUint8
	ParamUint16 = runtimeapi.ParamUint16
	ParamUint32 = runtimeapi.ParamUint32
	ParamUint64 = runtimeapi.ParamUint64
)

type ParamSpec = runtimeapi.ParamSpec

type AuthInfo struct {
	UID  string
	Data any
}

type Endpoint struct {
	Service               string
	Name                  string
	Access                Access
	Raw                   bool
	Path                  string
	Methods               []string
	Invoke                func(context.Context, []any, any) (any, error)
	RawHandler            func(http.ResponseWriter, *http.Request)
	DecodeContractRequest func(*http.Request, map[string]string) (ContractDecodedRequest, error)
	EncodeContractOutcome func(*http.Request, any) (ContractHTTPResponse, error)
	ContractPolicy        *ContractHTTPPolicy
	ContractPathTail      *ContractPathTail
}

type ContractPathTail struct {
	CanonicalTemplate string
	Name              string
	Target            string
	Type              string
	EmptyCapture      string
	MinimumSegments   int
	Decoding          string
	Guarantee         string
	Precedence        []string
	RequiredProfiles  []string
}

type ContractDecodedRequest struct {
	Payload  any
	PathArgs []any
}

type ContractHTTPResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}

type AuthHandler struct {
	Name         string
	Service      string
	Authenticate func(context.Context, string) (AuthInfo, error)
}

type CronJob struct {
	ID             string
	Title          string
	Every          time.Duration
	Schedule       string
	Calendar       string
	At             time.Time
	Timezone       string
	OverlapPolicy  string
	CatchupWindow  time.Duration
	PauseOnFailure bool
	Invoke         func(context.Context) error

	plan cronPlan
}

type DurableTask struct {
	Name                   string
	Service                string
	Version                int
	HandlerRef             string
	Handler                func(context.Context, []byte) ([]byte, error)
	DefaultTimeout         time.Duration
	DefaultLease           time.Duration
	MaxAttempts            int
	RetryInitial           time.Duration
	RetryMax               time.Duration
	RetryBackoff           float64
	RetryJitter            float64
	SuccessRetention       time.Duration
	FailureRetention       time.Duration
	MaxConcurrency         int
	DeduplicationRetention time.Duration
	DeduplicationConflict  string
	RequirementsJSON       string
}

type AppConfig struct {
	Name          string
	ListenAddr    string
	Observability ObservabilityConfig
	Role          string
}

type serviceShutdown struct {
	service  string
	order    int
	shutdown func(context.Context) error
}

type serviceInitializer struct {
	service      string
	dependencies []string
	initialize   func(context.Context) error
	shutdown     func(context.Context) error
}

type NativeServiceRegistration struct {
	Address      string
	Dependencies []string
	Initialize   func(context.Context) error
	Shutdown     func(context.Context) error
}

type registry struct {
	mu                        sync.RWMutex
	meta                      shared.AppMetadata
	endpoints                 map[string]*Endpoint
	authHandler               *AuthHandler
	cronJobs                  map[string]*CronJob
	durableTasks              map[string]*DurableTask
	contractDurableExecutions map[string]ContractDurableRegistration
	contractBindings          map[string]ContractInternalBindingRegistration
	contractCLIBindings       map[string]ContractCLIBindingRegistration
	contractPages             map[string]ContractPageRegistration
	contractEventBuses        map[string]ContractEventBus
	contractEventConsumers    map[string]ContractEventConsumerRegistration
	contractEventEmissions    map[string]ContractEventEmissionRegistration
	serviceInitializers       map[string]serviceInitializer
	serviceInitOrder          map[string]int
	serviceShutdowns          map[string]serviceShutdown
	observability             ObservabilityConfig
}

var global = &registry{
	endpoints:                 make(map[string]*Endpoint),
	cronJobs:                  make(map[string]*CronJob),
	durableTasks:              make(map[string]*DurableTask),
	contractDurableExecutions: make(map[string]ContractDurableRegistration),
	contractBindings:          make(map[string]ContractInternalBindingRegistration),
	contractCLIBindings:       make(map[string]ContractCLIBindingRegistration),
	contractPages:             make(map[string]ContractPageRegistration),
	contractEventBuses:        make(map[string]ContractEventBus),
	contractEventConsumers:    make(map[string]ContractEventConsumerRegistration),
	contractEventEmissions:    make(map[string]ContractEventEmissionRegistration),
	serviceInitializers:       make(map[string]serviceInitializer),
	serviceInitOrder:          make(map[string]int),
	serviceShutdowns:          make(map[string]serviceShutdown),
	meta: shared.AppMetadata{
		Environment: defaultEnvironment(),
	},
}

func SetAppConfig(cfg AppConfig) {
	global.mu.Lock()
	defer global.mu.Unlock()
	baseAppID := strings.TrimSpace(envpolicy.Get("SCENERY_BASE_APP_ID"))
	if baseAppID == "" {
		baseAppID = cfg.Name
	}
	runtimeAppID := strings.TrimSpace(envpolicy.Get("SCENERY_RUNTIME_APP_ID"))
	if runtimeAppID == "" {
		runtimeAppID = baseAppID
	}
	global.meta.AppID = cfg.Name
	global.meta.BaseAppID = baseAppID
	global.meta.RuntimeAppID = runtimeAppID
	global.meta.SessionID = strings.TrimSpace(envpolicy.Get("SCENERY_SESSION_ID"))
	global.meta.Environment = defaultEnvironment()
	global.observability = cfg.Observability
	if publicBaseURL := strings.TrimSpace(envpolicy.Get("SCENERY_PUBLIC_BASE_URL")); publicBaseURL != "" {
		global.meta.APIBaseURL = publicBaseURL
		return
	}
	global.meta.APIBaseURL = "http://" + cfg.ListenAddr
}

func SetPublicBaseURL(baseURL string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.meta.APIBaseURL = baseURL
}

func Meta() *shared.AppMetadata {
	global.mu.RLock()
	defer global.mu.RUnlock()
	meta := global.meta
	return &meta
}

func defaultEnvironment() shared.Environment {
	if strings.EqualFold(strings.TrimSpace(envpolicy.Get("SCENERY_RUNTIME_ENV")), "test") {
		return shared.Environment{
			Name:  "test",
			Type:  shared.EnvTest,
			Cloud: shared.CloudLocal,
		}
	}
	return shared.Environment{
		Name:  "local",
		Type:  shared.EnvDevelopment,
		Cloud: shared.CloudLocal,
	}
}

func RegisterEndpoint(ep *Endpoint) {
	if err := RegisterEndpointChecked(ep); err != nil {
		panic(err)
	}
}

func RegisterEndpointChecked(ep *Endpoint) error {
	if ep == nil {
		return fmt.Errorf("runtime: endpoint registration is nil")
	}
	key := endpointKey(ep.Service, ep.Name)
	if strings.TrimSpace(ep.Service) == "" || strings.TrimSpace(ep.Name) == "" {
		return fmt.Errorf("runtime: endpoint registration is missing service or name")
	}
	if err := validateContractHTTPPolicy(ep.ContractPolicy); err != nil {
		return fmt.Errorf("runtime: endpoint %s contract policy: %w", key, err)
	}
	if err := validateContractPathTail(ep); err != nil {
		return fmt.Errorf("runtime: endpoint %s path tail: %w", key, err)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.endpoints[key]; exists {
		return fmt.Errorf("runtime: duplicate endpoint registration for %s", key)
	}
	if len(ep.Methods) == 0 {
		return fmt.Errorf("runtime: endpoint %s missing methods", key)
	}
	if !ep.Raw && (ep.DecodeContractRequest == nil || ep.EncodeContractOutcome == nil || ep.Invoke == nil) {
		return fmt.Errorf("runtime: endpoint %s missing contract codec or implementation", key)
	}
	for existingKey, existing := range global.endpoints {
		if contractRouteConflict(ep, existing) {
			return fmt.Errorf("runtime: endpoint %s conflicts with route registered by %s", key, existingKey)
		}
	}
	global.endpoints[key] = ep
	return nil
}

func RegisterAuthHandler(handler *AuthHandler) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.authHandler != nil {
		panic("runtime: auth handler already registered")
	}
	global.authHandler = handler
}

func RegisterCronJobChecked(job *CronJob) error {
	global.mu.Lock()
	defer global.mu.Unlock()
	if err := validateCronJob(job); err != nil {
		return err
	}
	if _, exists := global.cronJobs[job.ID]; exists {
		return fmt.Errorf("runtime: duplicate cron job registration for %s", job.ID)
	}
	global.cronJobs[job.ID] = job
	return nil
}

func RegisterDurableTaskChecked(task *DurableTask) error {
	if task == nil {
		return fmt.Errorf("runtime: durable task cannot be nil")
	}
	task.Name = strings.TrimSpace(task.Name)
	task.Service = strings.TrimSpace(task.Service)
	if task.Name == "" {
		return fmt.Errorf("runtime: durable task name must not be empty")
	}
	if task.Service == "" {
		return fmt.Errorf("runtime: durable task %s service must not be empty", task.Name)
	}
	if task.Handler == nil {
		return fmt.Errorf("runtime: durable task %s handler must not be nil", task.Name)
	}
	if task.Version < 0 {
		return fmt.Errorf("runtime: durable task %s version must not be negative", task.Name)
	}
	if task.Version == 0 {
		task.Version = 1
	}
	if task.DeduplicationConflict == "" {
		task.DeduplicationConflict = "return_existing"
	}
	if task.DeduplicationConflict != "return_existing" {
		return fmt.Errorf("runtime: durable task %s has unsupported deduplication conflict policy %q", task.Name, task.DeduplicationConflict)
	}
	key := task.Service + ":" + task.Name
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.durableTasks[key]; exists {
		return fmt.Errorf("runtime: duplicate durable task registration for %s", key)
	}
	cp := *task
	global.durableTasks[key] = &cp
	return nil
}

func RegisterNativeService(registration NativeServiceRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	if registration.Address == "" {
		return fmt.Errorf("runtime: service initializer missing service name")
	}
	if registration.Initialize == nil {
		return fmt.Errorf("runtime: service initializer for %s is nil", registration.Address)
	}
	registration.Dependencies = canonicalContractAddresses(registration.Dependencies)
	for _, dependency := range registration.Dependencies {
		if dependency == registration.Address {
			return fmt.Errorf("runtime: service %s depends on itself", registration.Address)
		}
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.serviceInitializers[registration.Address]; exists {
		return fmt.Errorf("runtime: duplicate service initializer for %s", registration.Address)
	}
	global.serviceInitializers[registration.Address] = serviceInitializer{
		service: registration.Address, dependencies: registration.Dependencies,
		initialize: registration.Initialize, shutdown: registration.Shutdown,
	}
	return nil
}

func MarkServiceInitialized(service string, shutdown func(context.Context)) {
	var wrapped func(context.Context) error
	if shutdown != nil {
		wrapped = func(ctx context.Context) error { shutdown(ctx); return nil }
	}
	MarkServiceInitializedWithError(service, wrapped)
}

func MarkServiceInitializedWithError(service string, shutdown func(context.Context) error) {
	if strings.TrimSpace(service) == "" {
		panic("runtime: service shutdown missing service name")
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	global.serviceShutdowns[service] = serviceShutdown{
		service:  service,
		order:    global.serviceInitOrder[service],
		shutdown: shutdown,
	}
}

func listEndpoints() []*Endpoint {
	global.mu.RLock()
	defer global.mu.RUnlock()
	result := make([]*Endpoint, 0, len(global.endpoints))
	for _, ep := range global.endpoints {
		result = append(result, ep)
	}
	slices.SortFunc(result, func(a, b *Endpoint) int {
		if a.Service == b.Service {
			return compare(a.Name, b.Name)
		}
		return compare(a.Service, b.Service)
	})
	return result
}

func getAuthHandler() *AuthHandler {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.authHandler
}

func listCronJobs() []*CronJob {
	global.mu.RLock()
	defer global.mu.RUnlock()
	result := make([]*CronJob, 0, len(global.cronJobs))
	for _, job := range global.cronJobs {
		result = append(result, job)
	}
	slices.SortFunc(result, func(a, b *CronJob) int {
		return compare(a.ID, b.ID)
	})
	return result
}

func listDurableTasks() []*DurableTask {
	global.mu.RLock()
	defer global.mu.RUnlock()
	result := make([]*DurableTask, 0, len(global.durableTasks))
	for _, task := range global.durableTasks {
		cp := *task
		result = append(result, &cp)
	}
	slices.SortFunc(result, func(a, b *DurableTask) int {
		if cmp := strings.Compare(a.Service, b.Service); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result
}

func InitializeServices() error {
	global.mu.RLock()
	initializers := make(map[string]serviceInitializer, len(global.serviceInitializers))
	for service, initializer := range global.serviceInitializers {
		initializers[service] = initializer
	}
	global.mu.RUnlock()
	for service, initializer := range initializers {
		for _, dependency := range initializer.dependencies {
			if _, ok := initializers[dependency]; !ok {
				return fmt.Errorf("initialize service %s: dependency %s is not registered", service, dependency)
			}
		}
	}
	global.mu.Lock()
	global.serviceInitOrder = make(map[string]int, len(initializers))
	global.mu.Unlock()
	completed := map[string]bool{}
	order := 0
	for len(completed) < len(initializers) {
		var ready []serviceInitializer
		for service, initializer := range initializers {
			if completed[service] {
				continue
			}
			dependenciesReady := true
			for _, dependency := range initializer.dependencies {
				if !completed[dependency] {
					dependenciesReady = false
					break
				}
			}
			if dependenciesReady {
				ready = append(ready, initializer)
			}
		}
		if len(ready) == 0 {
			return fmt.Errorf("initialize services: dependency cycle")
		}
		slices.SortFunc(ready, func(a, b serviceInitializer) int { return compare(a.service, b.service) })
		global.mu.Lock()
		for _, initializer := range ready {
			order++
			global.serviceInitOrder[initializer.service] = order
		}
		global.mu.Unlock()
		type initializationResult struct {
			service string
			err     error
		}
		results := make(chan initializationResult, len(ready))
		for _, initializer := range ready {
			go func() {
				err := initializer.initialize(context.Background())
				if err == nil && initializer.shutdown != nil {
					MarkServiceInitializedWithError(initializer.service, initializer.shutdown)
				}
				results <- initializationResult{service: initializer.service, err: err}
			}()
		}
		batch := make([]initializationResult, 0, len(ready))
		for range ready {
			batch = append(batch, <-results)
		}
		slices.SortFunc(batch, func(a, b initializationResult) int { return compare(a.service, b.service) })
		for _, result := range batch {
			if result.err != nil {
				return fmt.Errorf("initialize service %s: %w", result.service, result.err)
			}
			completed[result.service] = true
		}
	}
	return nil
}

func ShutdownServices(ctx context.Context) error {
	global.mu.RLock()
	hooks := make([]serviceShutdown, 0, len(global.serviceShutdowns))
	for _, hook := range global.serviceShutdowns {
		if hook.shutdown != nil {
			hooks = append(hooks, hook)
		}
	}
	global.mu.RUnlock()

	slices.SortFunc(hooks, func(a, b serviceShutdown) int {
		switch {
		case a.order > b.order:
			return -1
		case a.order < b.order:
			return 1
		default:
			return compare(b.service, a.service)
		}
	})

	var errsList []error
	for _, hook := range hooks {
		if ctx != nil && ctx.Err() != nil {
			errsList = append(errsList, ctx.Err())
			break
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					errsList = append(errsList, fmt.Errorf("shutdown service %s: panic: %v", hook.service, recovered))
				}
			}()
			if err := hook.shutdown(ctx); err != nil {
				errsList = append(errsList, fmt.Errorf("shutdown service %s: %w", hook.service, err))
			}
		}()
	}
	return errorsJoin(errsList...)
}

func endpointKey(service, name string) string {
	return service + "." + name
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func errorsJoin(errs ...error) error {
	var filtered []error
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	return errors.Join(filtered...)
}
