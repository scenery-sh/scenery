package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pbrazdil/onlava/errs"
	"github.com/pbrazdil/onlava/internal/runtimeapi"
	onlavamiddleware "github.com/pbrazdil/onlava/middleware"
	"github.com/pbrazdil/onlava/runtime/shared"
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
	MiddlewareIDs         []string
	PathParams            []ParamSpec
	PayloadType           reflect.Type
	ResponseType          reflect.Type
	WireID                string
	WireSchemaHash        string
	WireAvailable         bool
	WireUnsupportedReason string
	Invoke                func(context.Context, []any, any) (any, error)
	WireInvoke            func(context.Context, []any, []byte) (any, error)
	WireInvokeJSON        func(context.Context, []any, []byte) ([]byte, error)
	RawHandler            func(http.ResponseWriter, *http.Request)
}

type Middleware struct {
	ID     string
	Invoke func(onlavamiddleware.Request, onlavamiddleware.Next) onlavamiddleware.Response
}

type AuthHandler struct {
	Name         string
	Service      string
	ParamType    reflect.Type
	AuthDataType reflect.Type
	Authenticate func(context.Context, any) (AuthInfo, error)
}

type CronJob struct {
	ID                   string
	Title                string
	Every                time.Duration
	Schedule             string
	OverlapPolicy        string
	CatchupWindow        time.Duration
	PauseOnFailure       bool
	ActivityStartToClose time.Duration
	ActivityRetryPolicy  CronRetryPolicy
	Invoke               func(context.Context) error

	plan cronPlan
}

type CronRetryPolicy struct {
	InitialInterval        time.Duration
	BackoffCoefficient     float64
	MaximumInterval        time.Duration
	MaximumAttempts        int32
	NonRetryableErrorTypes []string
}

type AppConfig struct {
	Name              string
	Workspace         string
	ListenAddr        string
	EnableDBStudio    bool
	ProxyAPIHost      string
	ProxyConsoleHost  string
	ProxyMCPHost      string
	ProxyTemporalHost string
	ProxyGrafanaHost  string
	ProxyFrontends    map[string]ProxyFrontendConfig
	Observability     ObservabilityConfig
	Temporal          TemporalConfig
	Role              string
}

type ProxyFrontendConfig struct {
	Host     string
	Root     string
	Upstream string
}

type serviceShutdown struct {
	service  string
	order    int
	shutdown func(context.Context)
}

type registry struct {
	mu                  sync.RWMutex
	meta                shared.AppMetadata
	endpoints           map[string]*Endpoint
	middlewares         map[string]*Middleware
	authHandler         *AuthHandler
	cronJobs            map[string]*CronJob
	serviceInitializers map[string]func() error
	serviceInitOrder    map[string]int
	serviceShutdowns    map[string]serviceShutdown
	observability       ObservabilityConfig
}

var global = &registry{
	endpoints:           make(map[string]*Endpoint),
	middlewares:         make(map[string]*Middleware),
	cronJobs:            make(map[string]*CronJob),
	serviceInitializers: make(map[string]func() error),
	serviceInitOrder:    make(map[string]int),
	serviceShutdowns:    make(map[string]serviceShutdown),
	meta: shared.AppMetadata{
		Environment: defaultEnvironment(),
	},
}

func SetAppConfig(cfg AppConfig) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.meta.AppID = cfg.Name
	global.meta.Environment = defaultEnvironment()
	global.observability = cfg.Observability
	if publicBaseURL := strings.TrimSpace(os.Getenv("ONLAVA_PUBLIC_BASE_URL")); publicBaseURL != "" {
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
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ONLAVA_RUNTIME_ENV")), "test") {
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
	key := endpointKey(ep.Service, ep.Name)
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.endpoints[key]; exists {
		panic(fmt.Sprintf("runtime: duplicate endpoint registration for %s", key))
	}
	if len(ep.Methods) == 0 {
		panic(fmt.Sprintf("runtime: endpoint %s missing methods", key))
	}
	if strings.TrimSpace(ep.WireID) == "" {
		ep.WireID = key
	}
	global.endpoints[key] = ep
}

func RegisterMiddleware(mw *Middleware) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.middlewares[mw.ID]; exists {
		panic(fmt.Sprintf("runtime: duplicate middleware registration for %s", mw.ID))
	}
	global.middlewares[mw.ID] = mw
}

func RegisterAuthHandler(handler *AuthHandler) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.authHandler != nil {
		panic("runtime: auth handler already registered")
	}
	global.authHandler = handler
}

func RegisterCronJob(job *CronJob) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if err := validateCronJob(job); err != nil {
		panic(err)
	}
	if _, exists := global.cronJobs[job.ID]; exists {
		panic(fmt.Sprintf("runtime: duplicate cron job registration for %s", job.ID))
	}
	global.cronJobs[job.ID] = job
}

func RegisterServiceInitializer(service string, init func() error) {
	if strings.TrimSpace(service) == "" {
		panic("runtime: service initializer missing service name")
	}
	if init == nil {
		panic(fmt.Sprintf("runtime: service initializer for %s is nil", service))
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.serviceInitializers[service]; exists {
		panic(fmt.Sprintf("runtime: duplicate service initializer for %s", service))
	}
	global.serviceInitializers[service] = init
}

func MarkServiceInitialized(service string, shutdown func(context.Context)) {
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

func LookupEndpoint(service, name string) (*Endpoint, bool) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	ep, ok := global.endpoints[endpointKey(service, name)]
	return ep, ok
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

func getMiddlewares(ids []string) ([]*Middleware, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	result := make([]*Middleware, 0, len(ids))
	for _, id := range ids {
		mw, ok := global.middlewares[id]
		if !ok {
			return nil, errs.B().Code(errs.Internal).Msgf("middleware %q not registered", id).Err()
		}
		result = append(result, mw)
	}
	return result, nil
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

func InitializeServices() error {
	global.mu.RLock()
	initializers := make([]struct {
		service string
		init    func() error
	}, 0, len(global.serviceInitializers))
	for service, init := range global.serviceInitializers {
		initializers = append(initializers, struct {
			service string
			init    func() error
		}{service: service, init: init})
	}
	global.mu.RUnlock()

	slices.SortFunc(initializers, func(a, b struct {
		service string
		init    func() error
	}) int {
		return compare(a.service, b.service)
	})

	global.mu.Lock()
	global.serviceInitOrder = make(map[string]int, len(initializers))
	for i, initializer := range initializers {
		global.serviceInitOrder[initializer.service] = i + 1
	}
	global.mu.Unlock()

	results := make(chan error, len(initializers))
	for _, initializer := range initializers {
		initializer := initializer
		go func() {
			if err := initializer.init(); err != nil {
				results <- fmt.Errorf("initialize service %s: %w", initializer.service, err)
				return
			}
			results <- nil
		}()
	}
	for range initializers {
		if err := <-results; err != nil {
			return err
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
			hook.shutdown(ctx)
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

func CallEndpoint(ctx context.Context, service, name string, pathArgs []any, payload any) (any, error) {
	ep, ok := LookupEndpoint(service, name)
	if !ok {
		return nil, errs.B().Code(errs.NotFound).Msgf("endpoint %s.%s not found", service, name).Err()
	}
	if ep.Raw {
		return nil, errs.B().Code(errs.Unimplemented).Msg("raw endpoints cannot be called internally").Err()
	}

	state := stateFromContext(ctx)
	started := time.Now()
	requestType := shared.InternalCall
	headers := make(http.Header)
	cronKey := ""
	if state != nil {
		if state.request.CronIdempotencyKey != "" {
			requestType = shared.APICall
			started = state.request.Started
			headers = state.request.Headers.Clone()
			cronKey = state.request.CronIdempotencyKey
		}
	}
	reqState := &requestState{
		started: started,
		request: shared.Request{
			Type:               requestType,
			Started:            started,
			Service:            ep.Service,
			Endpoint:           ep.Name,
			Method:             "INTERNAL",
			Path:               ep.Path,
			Headers:            headers,
			Payload:            payload,
			PathParams:         encodePathParams(ep.PathParams, pathArgs),
			CronIdempotencyKey: cronKey,
			API: &shared.APIDesc{
				RequestType:  ep.PayloadType,
				ResponseType: ep.ResponseType,
				Raw:          false,
				Exposed:      ep.Access != Private,
				AuthRequired: ep.Access == Auth,
			},
		},
	}
	reqState.logsEnabled = logsEnabledForRequest(reqState.request)
	reqState.traceEnabled = traceEnabledForRequest(reqState.request)
	if state != nil {
		reqState.auth = state.auth
		reqState.logsEnabled = state.logsEnabled && reqState.logsEnabled
		reqState.traceEnabled = state.traceEnabled && reqState.traceEnabled
		if state.request.CronIdempotencyKey != "" {
			reqState.request.Method = "CRON"
		}
		startInternalCallTrace(state, reqState)
	}
	if ep.Access == Auth && reqState.auth.UID == "" {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("endpoint requires auth").Err()
	}

	callCtx := context.WithValue(ctx, requestStateKey{}, reqState)
	restore := enterState(reqState)
	defer restore()

	resp, _, _, err := executeTypedEndpoint(ep, callCtx, pathArgs, payload)
	finishRequestTrace(reqState, errs.HTTPStatus(err), resp, err)
	return resp, err
}

func TypeOf[T any]() reflect.Type {
	return reflect.TypeFor[T]()
}

func RecordServiceInit(service string, duration time.Duration, err error) {
	recordServiceInit(service, duration, err)
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

func encodePathParams(specs []ParamSpec, values []any) shared.PathParams {
	params := make(shared.PathParams, 0, len(specs))
	for i, spec := range specs {
		if i >= len(values) {
			break
		}
		params = append(params, shared.PathParam{
			Name:  spec.Name,
			Value: fmt.Sprint(values[i]),
		})
	}
	return params
}
