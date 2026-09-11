package authbridge

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/runtimeapp"
)

// Endpoint is standard auth's native registration, including its typed callbacks.
// It is consumed in-process; callbacks and authentication data never become RPC.
type Endpoint struct {
	Service    string
	Name       string
	Access     runtimeapi.Access
	Path       string
	Methods    []string
	Raw        bool
	RawHandler func(http.ResponseWriter, *http.Request)
	Decode     func(*http.Request, map[string]string) (DecodedRequest, error)
	Invoke     func(context.Context, []any, any) (any, error)
	Encode     func(*http.Request, any) (Response, error)
}

type DecodedRequest struct {
	Payload  any
	PathArgs []any
}

type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// Runtime supplies the existing HTTP codec and registration/lifecycle owners to
// standard auth. Application auth helpers do not import the concrete host.
type Runtime interface {
	RegisterAuthenticator(func(context.Context, string) (runtimeapp.AuthInfo, error))
	RegisterEndpoint(Endpoint)
	MarkServiceInitialized(func(context.Context))
	DecodeJSON(*http.Request, any) error
	EncodeJSON(int, any) (Response, error)
}

type runtimeBinding struct{ runtime Runtime }

type runtimeOwner struct {
	bound   atomic.Pointer[runtimeBinding]
	mu      sync.Mutex
	pending []func(Runtime)
}

var owner runtimeOwner

func BindRuntime(runtime Runtime) { owner.bind(runtime) }

func (owner *runtimeOwner) bind(runtime Runtime) {
	if runtime == nil {
		panic("scenery: nil standard auth runtime")
	}
	owner.mu.Lock()
	if !owner.bound.CompareAndSwap(nil, &runtimeBinding{runtime: runtime}) {
		owner.mu.Unlock()
		panic("scenery: standard auth runtime is already bound")
	}
	pending := owner.pending
	owner.pending = nil
	owner.mu.Unlock()
	for _, register := range pending {
		register(runtime)
	}
}

// WhenRuntime retains native registrations made by application init functions
// until the single host is initialized. It does not initialize services.
func WhenRuntime(register func(Runtime)) { owner.whenReady(register) }

func (owner *runtimeOwner) whenReady(register func(Runtime)) {
	owner.mu.Lock()
	binding := owner.bound.Load()
	if binding == nil {
		owner.pending = append(owner.pending, register)
		owner.mu.Unlock()
		return
	}
	owner.mu.Unlock()
	register(binding.runtime)
}

func CurrentRuntime() Runtime {
	if binding := owner.bound.Load(); binding != nil {
		return binding.runtime
	}
	return nil
}
