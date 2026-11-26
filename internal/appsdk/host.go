package appsdk

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
)

// Host is the runtime implementation that application-facing packages
// (scenery.sh/auth, scenery.sh/durable) reach through this layer, so they never
// import the runtime and its dependency closure. The runtime registers itself
// during its package initialization, which completes before any application
// code runs in a Scenery executable.
type Host interface {
	CurrentAuth() (Auth, bool)
	WithAuth(context.Context, Auth) context.Context
	MarkServiceInitialized(service string, shutdown func(context.Context))
	TraceHTTPTransport(http.RoundTripper) http.RoundTripper
	TraceStorageOperation(ctx context.Context, store, operation string) (context.Context, func(int64, error))
	TraceDBQueryStart(ctx context.Context, query string, argsCount int) context.Context
	TraceDBQueryEnd(ctx context.Context, commandTag string, rowsAffected int64, err error)
	// FrameworkConfigSecret and FrameworkConfigString read a framework-owned
	// input (such as auth.jwt_secret) from the process's configuration
	// snapshot; the boolean reports whether the environment configured it.
	FrameworkConfigSecret(key string) ([]byte, bool, error)
	FrameworkConfigString(key string) (string, bool, error)
	ConnectionToken(context.Context, string, string, []string) (string, error)
}

// Auth is the authenticated principal of the current request.
type Auth struct {
	UID  string
	Data any
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

var configReloadHooks struct {
	sync.RWMutex
	byName map[string]func() error
}

// RegisterConfigReloadHook registers a named callback invoked after the Host
// replaces its runtime configuration snapshot.
func RegisterConfigReloadHook(name string, hook func() error) {
	if name == "" || hook == nil {
		return
	}
	configReloadHooks.Lock()
	if configReloadHooks.byName == nil {
		configReloadHooks.byName = make(map[string]func() error)
	}
	configReloadHooks.byName[name] = hook
	configReloadHooks.Unlock()
}

// NotifyConfigReload runs registered configuration reload hooks in name order.
func NotifyConfigReload() error {
	configReloadHooks.RLock()
	if len(configReloadHooks.byName) == 0 {
		configReloadHooks.RUnlock()
		return nil
	}
	names := make([]string, 0, len(configReloadHooks.byName))
	for name := range configReloadHooks.byName {
		names = append(names, name)
	}
	slices.Sort(names)
	hooks := make([]func() error, 0, len(names))
	for _, name := range names {
		hooks = append(hooks, configReloadHooks.byName[name])
	}
	configReloadHooks.RUnlock()
	for _, hook := range hooks {
		if err := hook(); err != nil {
			return err
		}
	}
	return nil
}
