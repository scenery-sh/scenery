package appsdk

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"scenery.sh/internal/envfile"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeapi"
)

// Host is the runtime implementation that application-facing packages
// (scenery.sh/auth, scenery.sh/durable) reach through this layer, so they never
// import the runtime and its dependency closure. The runtime registers itself
// during its package initialization, which completes before any application
// code runs in a Scenery executable.
type Host interface {
	CurrentAuth() (Auth, bool)
	WithAuth(context.Context, Auth) context.Context
	RegisterAuthHandler(AuthHandler)
	RegisterEndpoint(Endpoint)
	MarkServiceInitialized(service string, shutdown func(context.Context))
	DecodeJSON(request *http.Request, target any) error
	EncodeJSON(status int, value any) (Response, error)
	DurableSignal(ctx context.Context, service, jobID, name, dedupeKey string, payload []byte) error
	DurableStep(ctx context.Context, key string, run func(context.Context) ([]byte, error)) ([]byte, error)
}

// Auth is the authenticated principal of the current request.
type Auth struct {
	UID  string
	Data any
}

// AuthHandler authenticates a bearer token for the application.
type AuthHandler struct {
	Service      string
	Name         string
	Authenticate func(context.Context, string) (Auth, error)
}

// Endpoint is a framework-owned HTTP endpoint. A non-nil RawHandler serves the
// request directly; otherwise Decode, Invoke and Encode form a typed contract
// endpoint.
type Endpoint struct {
	Service    string
	Name       string
	Access     runtimeapi.Access
	Path       string
	Methods    []string
	RawHandler func(http.ResponseWriter, *http.Request)
	Decode     func(request *http.Request, pathValues map[string]string) (payload any, pathArgs []any, err error)
	Invoke     func(ctx context.Context, pathArgs []any, payload any) (any, error)
	Encode     func(request *http.Request, outcome any) (Response, error)
}

// Response is an encoded contract answer.
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// ErrNoHost reports a call that needs the runtime in a process that did not
// link it.
var ErrNoHost = errors.New("scenery runtime is not linked into this process")

var host struct {
	sync.RWMutex
	value Host
}

// RegisterHost installs the runtime implementation.
func RegisterHost(value Host) {
	host.Lock()
	host.value = value
	host.Unlock()
}

// CurrentHost returns the registered runtime, or nil when none is linked.
func CurrentHost() Host {
	host.RLock()
	defer host.RUnlock()
	return host.value
}

var dotEnv struct {
	once sync.Once
	data map[string]string
	err  error
}

// LoadDotEnv sets each variable of the working directory's .env file that the
// process environment does not already define. The file is read once.
func LoadDotEnv() error {
	dotEnv.once.Do(func() { dotEnv.data, dotEnv.err = envfile.ParseFile(".env") })
	if dotEnv.err != nil {
		return dotEnv.err
	}
	for key, value := range dotEnv.data {
		if _, exists := envpolicy.Lookup(key); exists {
			continue
		}
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}
