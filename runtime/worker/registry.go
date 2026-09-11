// Package worker owns the native feasibility experiment in plan 0180. It is
// generated bootstrap infrastructure, not a second production runtime mode.
package worker

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"scenery.sh/internal/nativecall"
	"scenery.sh/internal/nativecompose"
	"scenery.sh/internal/nativeservice"
	"scenery.sh/internal/nativesql"
	"scenery.sh/internal/runtimeapp"
	"scenery.sh/runtime/shared"
)

const ContractRuntimeABI = nativecompose.RuntimeABI

type ContractRegistration = nativecompose.Registration
type ContractRegistryOptions = nativecompose.Options
type NativeServiceRegistration = nativeservice.Registration

// Operation retains every native method, its exact codecs, and streaming ABI.
// Transport admission is separate; registration alone never grants execution.
type Operation struct {
	Address   string
	Service   string
	Streaming bool
	Decode    func([]byte) (any, error)
	Encode    func(any) ([]byte, error)
	Invoke    func(context.Context, any) (any, runtimeapp.ByteStream, error)
}

type Registry struct {
	metadata   shared.AppMetadata
	mu         sync.RWMutex
	services   *nativeservice.Registry
	operations map[string]Operation
	bindings   *nativecall.Registry
	sealed     bool
}

func NewRegistry() *Registry {
	return &Registry{metadata: shared.AppMetadata{Environment: runtimeapp.DefaultEnvironment()}, services: &nativeservice.Registry{}, operations: map[string]Operation{}, bindings: &nativecall.Registry{}}
}

var application = NewRegistry()

func RegisterNativeService(registration NativeServiceRegistration) error {
	return application.RegisterService(registration)
}

func RegisterOperation(operation Operation) error { return application.RegisterOperation(operation) }

func (registry *Registry) RegisterService(registration NativeServiceRegistration) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return fmt.Errorf("native worker composition is sealed")
	}
	return registry.services.Register(registration)
}

func (registry *Registry) RegisterOperation(operation Operation) error {
	if operation.Address == "" || operation.Service == "" || operation.Decode == nil || operation.Encode == nil || operation.Invoke == nil {
		return fmt.Errorf("native worker operation requires identity and native codecs/callback")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return fmt.Errorf("native worker composition is sealed")
	}
	if _, exists := registry.operations[operation.Address]; exists {
		return fmt.Errorf("duplicate native worker operation %s", operation.Address)
	}
	registry.operations[operation.Address] = operation
	return nil
}

func (registry *Registry) operation(address string) (Operation, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	operation, exists := registry.operations[address]
	return operation, exists
}

type ContractRegistry struct{ *nativecompose.Registry }

func NewContractRegistry(options ContractRegistryOptions) (*ContractRegistry, error) {
	registry, err := nativecompose.New(options)
	if err != nil {
		return nil, err
	}
	return &ContractRegistry{registry}, nil
}

func (registry *ContractRegistry) Seal() error {
	return registry.Registry.Seal(func() nativecompose.Transaction {
		application.mu.RLock()
		services := application.services.Snapshot()
		operations := maps.Clone(application.operations)
		application.mu.RUnlock()
		return nativecompose.Transaction{
			Rollback: func() {
				application.mu.Lock()
				defer application.mu.Unlock()
				application.services = nativeservice.Restore(services)
				application.operations = operations
			},
			Validate: application.seal,
		}
	})
}

func (registry *Registry) seal() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return fmt.Errorf("native worker composition is sealed")
	}
	for _, operation := range registry.operations {
		if !registry.services.Has(operation.Service) {
			return fmt.Errorf("native worker operation %s has no service", operation.Address)
		}
	}
	registry.sealed = true
	return nil
}

// Injected internal clients still use native Go calls. Bindings not converted
// by the experiment fail explicitly in the native registry, never via RPC.
func InvokeContractBindingFrom(ctx context.Context, address, caller string, invocation, input any) (any, error) {
	return application.bindings.InvokeFrom(ctx, address, caller, invocation, input)
}

type SQLBinding = nativesql.Binding

func ConfigureSQLBindings(bindings []SQLBinding) error { return nativesql.Configure(bindings) }

func (registry *Registry) metadataSnapshot() *shared.AppMetadata {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	value := registry.metadata
	return &value
}
