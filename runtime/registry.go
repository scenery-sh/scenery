package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"pulse.dev/errs"
	pulsemiddleware "pulse.dev/middleware"
	"pulse.dev/runtime/shared"
)

type Access string

const (
	Public  Access = "public"
	Auth    Access = "auth"
	Private Access = "private"
)

type ParamKind string

const (
	ParamString ParamKind = "string"
	ParamBool   ParamKind = "bool"
	ParamInt    ParamKind = "int"
	ParamInt8   ParamKind = "int8"
	ParamInt16  ParamKind = "int16"
	ParamInt32  ParamKind = "int32"
	ParamInt64  ParamKind = "int64"
	ParamUint   ParamKind = "uint"
	ParamUint8  ParamKind = "uint8"
	ParamUint16 ParamKind = "uint16"
	ParamUint32 ParamKind = "uint32"
	ParamUint64 ParamKind = "uint64"
)

type ParamSpec struct {
	Name string
	Kind ParamKind
}

type AuthInfo struct {
	UID  string
	Data any
}

type Endpoint struct {
	Service       string
	Name          string
	Access        Access
	Raw           bool
	Path          string
	Methods       []string
	MiddlewareIDs []string
	PathParams    []ParamSpec
	PayloadType   reflect.Type
	ResponseType  reflect.Type
	Invoke        func(context.Context, []any, any) (any, error)
	RawHandler    func(http.ResponseWriter, *http.Request)
}

type Middleware struct {
	ID     string
	Invoke func(pulsemiddleware.Request, pulsemiddleware.Next) pulsemiddleware.Response
}

type AuthHandler struct {
	Name         string
	Service      string
	ParamType    reflect.Type
	AuthDataType reflect.Type
	Authenticate func(context.Context, any) (AuthInfo, error)
}

type CronJob struct {
	ID       string
	Title    string
	Every    time.Duration
	Schedule string
	Invoke   func(context.Context) error

	plan cronPlan
}

type AppConfig struct {
	Name              string
	Workspace         string
	ListenAddr        string
	ProxyAPIHost      string
	ProxyConsoleHost  string
	ProxyMCPHost      string
	ProxyFrontendHost string
}

type registry struct {
	mu          sync.RWMutex
	meta        shared.AppMetadata
	endpoints   map[string]*Endpoint
	middlewares map[string]*Middleware
	authHandler *AuthHandler
	cronJobs    map[string]*CronJob
}

var global = &registry{
	endpoints:   make(map[string]*Endpoint),
	middlewares: make(map[string]*Middleware),
	cronJobs:    make(map[string]*CronJob),
	meta: shared.AppMetadata{
		Environment: shared.Environment{
			Name:  "local",
			Type:  shared.EnvDevelopment,
			Cloud: shared.CloudLocal,
		},
	},
}

func SetAppConfig(cfg AppConfig) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.meta.AppID = cfg.Name
	if publicBaseURL := strings.TrimSpace(os.Getenv("PULSE_PUBLIC_BASE_URL")); publicBaseURL != "" {
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
	if state != nil {
		reqState.auth = state.auth
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
