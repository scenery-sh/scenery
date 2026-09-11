// Package nativeservice owns native service registration and lifecycle. It does
// not import the runtime host; constructors and shutdowns remain Go callbacks.
package nativeservice

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

type Registration struct {
	Address      string
	Dependencies []string
	Initialize   func(context.Context) error
	Shutdown     func(context.Context) error
}

type serviceShutdown struct {
	service  string
	order    int
	shutdown func(context.Context) error
}

type Registry struct {
	mu           sync.RWMutex
	initializers map[string]Registration
	initOrder    map[string]int
	shutdowns    map[string]serviceShutdown
}

// Snapshot retains the native callback set for a composition transaction.
// Dependency and order maps are copies; callbacks retain their service objects.
type Snapshot struct {
	Initializers map[string]Registration
	InitOrder    map[string]int
	shutdowns    map[string]serviceShutdown
}

func (registry *Registry) Snapshot() Snapshot {
	if registry == nil {
		return Snapshot{}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	initializers := maps.Clone(registry.initializers)
	for address, registration := range initializers {
		registration.Dependencies = slices.Clone(registration.Dependencies)
		initializers[address] = registration
	}
	return Snapshot{Initializers: initializers, InitOrder: maps.Clone(registry.initOrder), shutdowns: maps.Clone(registry.shutdowns)}
}

func Restore(snapshot Snapshot) *Registry {
	registry := &Registry{initializers: snapshot.Initializers, initOrder: snapshot.InitOrder, shutdowns: snapshot.shutdowns}
	copy := registry.Snapshot()
	return &Registry{initializers: copy.Initializers, initOrder: copy.InitOrder, shutdowns: copy.shutdowns}
}

func canonicalAddresses(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func (registry *Registry) Register(registration Registration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	if registration.Address == "" {
		return fmt.Errorf("runtime: service initializer missing service name")
	}
	if registration.Initialize == nil {
		return fmt.Errorf("runtime: service initializer for %s is nil", registration.Address)
	}
	registration.Dependencies = canonicalAddresses(registration.Dependencies)
	if slices.Contains(registration.Dependencies, registration.Address) {
		return fmt.Errorf("runtime: service %s depends on itself", registration.Address)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.initializers[registration.Address]; exists {
		return fmt.Errorf("runtime: duplicate service initializer for %s", registration.Address)
	}
	if registry.initializers == nil {
		registry.initializers = make(map[string]Registration)
	}
	registry.initializers[registration.Address] = registration
	return nil
}

func (registry *Registry) Has(address string) bool {
	if registry == nil {
		return false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	_, exists := registry.initializers[address]
	return exists
}

// AddDependency augments a registered framework service before initialization.
// Missing services return false, preserving the host's optional bootstrap hook.
func (registry *Registry) AddDependency(address, dependency string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registration, ok := registry.initializers[address]
	if !ok {
		return false
	}
	registration.Dependencies = canonicalAddresses(append(slices.Clone(registration.Dependencies), dependency))
	registry.initializers[address] = registration
	return true
}

func (registry *Registry) MarkInitialized(service string, shutdown func(context.Context) error) {
	if strings.TrimSpace(service) == "" {
		panic("runtime: service shutdown missing service name")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.shutdowns == nil {
		registry.shutdowns = make(map[string]serviceShutdown)
	}
	registry.shutdowns[service] = serviceShutdown{service: service, order: registry.initOrder[service], shutdown: shutdown}
}

func (registry *Registry) Initialize(ctx context.Context) error {
	registry.mu.RLock()
	initializers := make(map[string]Registration, len(registry.initializers))
	maps.Copy(initializers, registry.initializers)
	registry.mu.RUnlock()
	for service, initializer := range initializers {
		for _, dependency := range initializer.Dependencies {
			if _, ok := initializers[dependency]; !ok {
				return fmt.Errorf("initialize service %s: dependency %s is not registered", service, dependency)
			}
		}
	}
	registry.mu.Lock()
	registry.initOrder = make(map[string]int, len(initializers))
	registry.mu.Unlock()
	completed := map[string]bool{}
	order := 0
	for len(completed) < len(initializers) {
		var ready []Registration
		for service, initializer := range initializers {
			if completed[service] {
				continue
			}
			dependenciesReady := true
			for _, dependency := range initializer.Dependencies {
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
		slices.SortFunc(ready, func(a, b Registration) int { return strings.Compare(a.Address, b.Address) })
		registry.mu.Lock()
		for _, initializer := range ready {
			order++
			registry.initOrder[initializer.Address] = order
		}
		registry.mu.Unlock()
		type initializationResult struct {
			service string
			err     error
		}
		results := make(chan initializationResult, len(ready))
		for _, initializer := range ready {
			go func() {
				err := initializer.Initialize(ctx)
				if err == nil && initializer.Shutdown != nil {
					registry.MarkInitialized(initializer.Address, initializer.Shutdown)
				}
				results <- initializationResult{service: initializer.Address, err: err}
			}()
		}
		batch := make([]initializationResult, 0, len(ready))
		for range ready {
			batch = append(batch, <-results)
		}
		slices.SortFunc(batch, func(a, b initializationResult) int { return strings.Compare(a.service, b.service) })
		for _, result := range batch {
			if result.err != nil {
				return fmt.Errorf("initialize service %s: %w", result.service, result.err)
			}
			completed[result.service] = true
		}
	}
	return nil
}

func (registry *Registry) Shutdown(ctx context.Context) error {
	registry.mu.RLock()
	hooks := make([]serviceShutdown, 0, len(registry.shutdowns))
	for _, hook := range registry.shutdowns {
		if hook.shutdown != nil {
			hooks = append(hooks, hook)
		}
	}
	registry.mu.RUnlock()

	slices.SortFunc(hooks, func(a, b serviceShutdown) int {
		switch {
		case a.order > b.order:
			return -1
		case a.order < b.order:
			return 1
		default:
			return strings.Compare(b.service, a.service)
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
	return errors.Join(errsList...)
}
