package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime/shared"
)

type AppConfig struct {
	Name       string
	ListenAddr string
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
	mu                    sync.RWMutex
	workerOperations      map[string]func(context.Context, []byte) ([]byte, shared.ByteStream, error)
	workerEventOperations map[string]func(context.Context, []byte) ([]byte, shared.RawEventProducer, error)
	serviceInitializers   map[string]serviceInitializer
	serviceInitOrder      map[string]int
	serviceShutdowns      map[string]serviceShutdown
}

var global = &registry{
	serviceInitializers: make(map[string]serviceInitializer),
	serviceInitOrder:    make(map[string]int),
	serviceShutdowns:    make(map[string]serviceShutdown),
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
	meta := shared.AppMetadata{
		AppID: cfg.Name, BaseAppID: baseAppID, RuntimeAppID: runtimeAppID,
		SessionID: strings.TrimSpace(envpolicy.Get("SCENERY_SESSION_ID")), Environment: appsdk.DefaultEnvironment(),
	}
	if publicBaseURL := strings.TrimSpace(envpolicy.Get("SCENERY_PUBLIC_BASE_URL")); publicBaseURL != "" {
		meta.APIBaseURL = publicBaseURL
		appsdk.SetMetadata(meta)
		return
	}
	meta.APIBaseURL = "http://" + cfg.ListenAddr
	appsdk.SetMetadata(meta)
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
	if slices.Contains(registration.Dependencies, registration.Address) {
		return fmt.Errorf("runtime: service %s depends on itself", registration.Address)
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

func InitializeServices() error {
	global.mu.RLock()
	initializers := make(map[string]serviceInitializer, len(global.serviceInitializers))
	maps.Copy(initializers, global.serviceInitializers)
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

// ShutdownServices shuts every registered service down in reverse
// initialization order. A deadline that passed does not skip the services left:
// each still runs with the expired context, which obliges it to release what it
// owns at once — a service that owns child processes stops them instead of
// leaving them running after the application exits.
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
	expired := false
	for _, hook := range hooks {
		if ctx != nil && ctx.Err() != nil && !expired {
			expired = true
			errsList = append(errsList, ctx.Err())
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
