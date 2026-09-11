package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/nativecall"
	"scenery.sh/internal/nativeservice"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/runtimeapp"
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

type AuthInfo = runtimeapp.AuthInfo

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
}

type ContractDecodedRequest struct {
	Payload  any
	PathArgs []any
}

type ContractHTTPResponse struct {
	Status         int
	Headers        http.Header
	Body           []byte
	Stream         *ContractByteStream
	StreamEncoding string
}

func (response *ContractHTTPResponse) Close() error {
	if response == nil || response.Stream == nil {
		return nil
	}
	err := response.Stream.Close()
	response.Stream = nil
	return err
}

// ContractByteStream is an exact-length response body whose reader ownership
// transfers to Scenery after a successful streaming handler return.
type ContractByteStream = runtimeapp.ByteStream

func NewContractByteStream(reader io.ReadCloser, size int64) ContractByteStream {
	return runtimeapp.NewByteStream(reader, size)
}

// ContractStreamOutcome keeps a typed operation outcome separate from its
// non-serializable response body until the selected HTTP response is encoded.
type ContractStreamOutcome struct {
	Outcome any
	stream  ContractByteStream
}

func NewContractStreamOutcome(outcome any, stream ContractByteStream) *ContractStreamOutcome {
	return &ContractStreamOutcome{Outcome: outcome, stream: stream}
}

func (outcome *ContractStreamOutcome) TakeStream() (ContractByteStream, error) {
	if outcome == nil || outcome.stream.Reader == nil {
		return ContractByteStream{}, fmt.Errorf("streaming outcome has no byte stream")
	}
	stream := outcome.stream
	outcome.stream.Reader = nil
	return stream, nil
}

func (outcome *ContractStreamOutcome) RequireNoStream() error {
	if outcome != nil && outcome.stream.Reader != nil {
		return fmt.Errorf("non-streaming outcome returned a byte stream")
	}
	return nil
}

func (outcome *ContractStreamOutcome) Close() error {
	if outcome == nil {
		return nil
	}
	return outcome.stream.Close()
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

type NativeServiceRegistration = nativeservice.Registration

type registry struct {
	mu                        sync.RWMutex
	meta                      shared.AppMetadata
	endpoints                 map[string]*Endpoint
	authHandler               *AuthHandler
	cronJobs                  map[string]*CronJob
	durableTasks              map[string]*DurableTask
	contractDurableExecutions map[string]ContractDurableRegistration
	contractBindings          *nativecall.Registry
	mcpTools                  map[string]MCPToolRegistration
	mcpFederationSpecs        map[string]MCPFederationRegistration
	mcpFederations            map[string]*mcpFederationState
	mcpFederationAssistants   map[string]string
	mcpSecretResolvers        map[string]MCPSecretResolver
	assistantMCPManifests     map[string]mcpcontract.Manifest
	assistants                map[string]AssistantRegistration
	assistantClients          map[string]AssistantClient
	contractCLIBindings       map[string]ContractCLIBindingRegistration
	contractPages             map[string]ContractPageRegistration
	contractEventBuses        map[string]ContractEventBus
	contractEventConsumers    map[string]ContractEventConsumerRegistration
	contractEventEmissions    map[string]ContractEventEmissionRegistration
	services                  *nativeservice.Registry
	observability             ObservabilityConfig
}

var global = &registry{
	endpoints:                 make(map[string]*Endpoint),
	cronJobs:                  make(map[string]*CronJob),
	durableTasks:              make(map[string]*DurableTask),
	contractDurableExecutions: make(map[string]ContractDurableRegistration),
	contractBindings:          &nativecall.Registry{},
	mcpTools:                  make(map[string]MCPToolRegistration),
	mcpFederationSpecs:        make(map[string]MCPFederationRegistration),
	mcpFederations:            make(map[string]*mcpFederationState),
	mcpFederationAssistants:   make(map[string]string),
	mcpSecretResolvers:        make(map[string]MCPSecretResolver),
	assistantMCPManifests:     make(map[string]mcpcontract.Manifest),
	assistants:                make(map[string]AssistantRegistration),
	assistantClients:          make(map[string]AssistantClient),
	contractCLIBindings:       make(map[string]ContractCLIBindingRegistration),
	contractPages:             make(map[string]ContractPageRegistration),
	contractEventBuses:        make(map[string]ContractEventBus),
	contractEventConsumers:    make(map[string]ContractEventConsumerRegistration),
	contractEventEmissions:    make(map[string]ContractEventEmissionRegistration),
	services:                  &nativeservice.Registry{},
	meta: shared.AppMetadata{
		Environment: defaultEnvironment(),
	},
}

func SetAppConfig(cfg AppConfig) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.meta = runtimeapp.ResolveMetadata(cfg.Name, cfg.ListenAddr)
	global.observability = cfg.Observability
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
	return runtimeapp.DefaultEnvironment()
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

func serviceRegistryLocked() *nativeservice.Registry {
	if global.services == nil {
		global.services = &nativeservice.Registry{}
	}
	return global.services
}

func currentServices() *nativeservice.Registry {
	global.mu.Lock()
	defer global.mu.Unlock()
	return serviceRegistryLocked()
}

func RegisterNativeService(registration NativeServiceRegistration) error {
	global.mu.Lock()
	defer global.mu.Unlock()
	return serviceRegistryLocked().Register(registration)
}

func MarkServiceInitialized(service string, shutdown func(context.Context)) {
	var wrapped func(context.Context) error
	if shutdown != nil {
		wrapped = func(ctx context.Context) error { shutdown(ctx); return nil }
	}
	MarkServiceInitializedWithError(service, wrapped)
}

func MarkServiceInitializedWithError(service string, shutdown func(context.Context) error) {
	currentServices().MarkInitialized(service, shutdown)
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

func InitializeServices() error { return currentServices().Initialize(context.Background()) }

func ShutdownServices(ctx context.Context) error { return currentServices().Shutdown(ctx) }

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
