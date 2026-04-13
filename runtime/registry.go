package runtime

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"time"

	"pulse.dev/errs"
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
	Service      string
	Name         string
	Access       Access
	Raw          bool
	Path         string
	Methods      []string
	PathParams   []ParamSpec
	PayloadType  reflect.Type
	ResponseType reflect.Type
	Invoke       func(context.Context, []any, any) (any, error)
	RawHandler   func(http.ResponseWriter, *http.Request)
}

type AuthHandler struct {
	Service      string
	ParamType    reflect.Type
	AuthDataType reflect.Type
	Authenticate func(context.Context, any) (AuthInfo, error)
}

type AppConfig struct {
	Name       string
	ListenAddr string
}

type registry struct {
	mu          sync.RWMutex
	meta        shared.AppMetadata
	endpoints   map[string]*Endpoint
	authHandler *AuthHandler
}

var global = &registry{
	endpoints: make(map[string]*Endpoint),
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
	global.meta.APIBaseURL = "http://" + cfg.ListenAddr
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

func RegisterAuthHandler(handler *AuthHandler) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.authHandler != nil {
		panic("runtime: auth handler already registered")
	}
	global.authHandler = handler
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
	reqState := &requestState{
		started: time.Now(),
		request: shared.Request{
			Type:       shared.InternalCall,
			Service:    ep.Service,
			Endpoint:   ep.Name,
			Method:     "INTERNAL",
			Path:       ep.Path,
			Headers:    make(http.Header),
			Payload:    payload,
			PathParams: encodePathParams(ep.PathParams, pathArgs),
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
	}
	if ep.Access == Auth && reqState.auth.UID == "" {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("endpoint requires auth").Err()
	}

	callCtx := context.WithValue(ctx, requestStateKey{}, reqState)
	restore := enterState(reqState)
	defer restore()

	return ep.Invoke(callCtx, pathArgs, payload)
}

func TypeOf[T any]() reflect.Type {
	return reflect.TypeFor[T]()
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
