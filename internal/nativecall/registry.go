// Package nativecall owns in-process internal dispatch. Native inputs, outputs,
// contexts and callback errors never cross a serialization boundary here.
package nativecall

import (
	"context"
	"fmt"
	"maps"
	"scenery.sh/internal/runtimeapi"
	"strings"
	"sync"
)

type Invoke func(context.Context, any, any) (any, error)

type Registration struct {
	Address      string
	Visibility   string
	Package      string
	DecodeInput  func([]byte) (any, error)
	EncodeOutput func(any) ([]byte, error)
	Invoke       Invoke
	SystemError  func(error) error
}

// Registry releases its lock before invoking any application code, so nested
// calls retain their native stack and can reenter the registry.
type Registry struct {
	mu       sync.RWMutex
	bindings map[string]Registration
}

func Normalize(registration Registration) (Registration, error) {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.Visibility = strings.TrimSpace(registration.Visibility)
	registration.Package = strings.TrimSpace(registration.Package)
	if registration.Address == "" || registration.Invoke == nil {
		return registration, fmt.Errorf("contract internal binding requires an address and invoke function")
	}
	if registration.Visibility == "" {
		registration.Visibility = "application"
	}
	if registration.Visibility != "application" && registration.Visibility != "package" {
		return registration, fmt.Errorf("contract internal binding %s has unsupported visibility %q", registration.Address, registration.Visibility)
	}
	if registration.Visibility == "package" && registration.Package == "" {
		return registration, fmt.Errorf("package-visible contract internal binding %s requires a package identity", registration.Address)
	}
	if registration.EncodeOutput != nil && registration.SystemError == nil {
		return registration, fmt.Errorf("contract internal binding %s requires a system error handler", registration.Address)
	}
	return registration, nil
}

func (registry *Registry) Register(registration Registration) error {
	registration, err := Normalize(registration)
	if err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.bindings == nil {
		registry.bindings = make(map[string]Registration)
	}
	if _, exists := registry.bindings[registration.Address]; exists {
		return fmt.Errorf("duplicate contract internal binding %s", registration.Address)
	}
	registry.bindings[registration.Address] = registration
	return nil
}

func (registry *Registry) lookup(address string) Registration {
	if registry == nil {
		return Registration{}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.bindings[address]
}

func (registry *Registry) Has(address string) bool { return registry.lookup(address).Invoke != nil }

// Clone snapshots registrations for the composition transaction's rollback.
// Function values remain native and preserve their captured service instances.
func (registry *Registry) Clone() *Registry {
	if registry == nil {
		return &Registry{}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return &Registry{bindings: maps.Clone(registry.bindings)}
}

func (registry *Registry) InvokeJSON(ctx context.Context, address, callerPackage string, input []byte) ([]byte, error) {
	registration := registry.lookup(address)
	if registration.Invoke == nil {
		return nil, fmt.Errorf("contract internal binding %s is not registered", address)
	}
	if registration.DecodeInput == nil || registration.EncodeOutput == nil {
		return nil, fmt.Errorf("capability_unavailable: contract internal binding %s has no JSON codec", address)
	}
	typed, err := registration.DecodeInput(input)
	if err != nil {
		return nil, fmt.Errorf("invalid_argument: decode internal binding input: %w", err)
	}
	invocation, ok := runtimeapi.InvocationFromContext(ctx)
	if !ok || !invocation.Valid() {
		return nil, fmt.Errorf("permission_denied: internal binding requires the current runtime invocation")
	}
	value, err := registry.invoke(ctx, address, callerPackage, invocation, typed, registration)
	if err != nil {
		return nil, err
	}
	encoded, err := registration.EncodeOutput(value)
	if err != nil {
		return nil, registration.SystemError(fmt.Errorf("encode internal binding output: %w", err))
	}
	return encoded, nil
}

func (registry *Registry) InvokeFrom(ctx context.Context, address, callerPackage string, invocation, input any) (any, error) {
	return registry.invoke(ctx, address, callerPackage, invocation, input, registry.lookup(address))
}

func (registry *Registry) invoke(ctx context.Context, address, callerPackage string, invocation, input any, registration Registration) (any, error) {
	if registration.Invoke == nil {
		return nil, fmt.Errorf("contract internal binding %s is not registered", address)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token, ok := invocation.(runtimeapi.Invocation)
	current, currentOK := runtimeapi.InvocationFromContext(ctx)
	if !ok || !token.Valid() || !currentOK || !runtimeapi.SameInvocation(token, current) {
		return nil, fmt.Errorf("permission_denied: internal binding requires the current runtime invocation")
	}
	if registration.Visibility == "package" && strings.TrimSpace(callerPackage) != registration.Package {
		return nil, fmt.Errorf("permission_denied: internal binding %s is visible only to package %s", address, registration.Package)
	}
	return registration.Invoke(ctx, invocation, input)
}
