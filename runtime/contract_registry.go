package runtime

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"scenery.sh/internal/runtimeapi"
)

const ContractRuntimeABI = "scenery.go-runtime/v1"

// ContractRegistration is the application-adapter payload accepted by the
// edition-2027 registration boundary. Apply is delayed until Seal has verified
// the complete ownership set, so missing or duplicate adapters cannot expose a
// partial runtime graph.
type ContractRegistration struct {
	ContractRevision           string
	PackageContractABIRevision string
	RuntimeABI                 string
	ProviderABIs               map[string]string
	CoveredAddresses           []string
	Apply                      func() error
}

type ContractRegistryOptions struct {
	ContractRevision  string
	RequiredAddresses []string
	ProviderABIs      map[string]string
}

type ContractRegistry struct {
	mu            sync.Mutex
	contract      string
	required      map[string]bool
	owners        map[string]string
	registrations map[string]ContractRegistration
	providerABIs  map[string]string
	sealed        bool
}

var _ runtimeapi.Registry = (*ContractRegistry)(nil)

func NewContractRegistry(options ContractRegistryOptions) (*ContractRegistry, error) {
	if strings.TrimSpace(options.ContractRevision) == "" {
		return nil, fmt.Errorf("runtime: contract registry requires contract_revision")
	}
	required := map[string]bool{}
	for _, address := range options.RequiredAddresses {
		address = strings.TrimSpace(address)
		if address == "" {
			return nil, fmt.Errorf("runtime: required contract address is empty")
		}
		if required[address] {
			return nil, fmt.Errorf("runtime: duplicate required contract address %s", address)
		}
		required[address] = true
	}
	return &ContractRegistry{
		contract: options.ContractRevision, required: required,
		owners: map[string]string{}, registrations: map[string]ContractRegistration{}, providerABIs: cloneContractStringMap(options.ProviderABIs),
	}, nil
}

func (registry *ContractRegistry) Register(address string, implementation any) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return fmt.Errorf("runtime: contract registry is sealed")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return fmt.Errorf("runtime: adapter address is empty")
	}
	if _, exists := registry.registrations[address]; exists {
		return fmt.Errorf("runtime: duplicate application adapter %s", address)
	}
	registration, ok := implementation.(ContractRegistration)
	if !ok {
		pointer, pointerOK := implementation.(*ContractRegistration)
		if !pointerOK || pointer == nil {
			return fmt.Errorf("runtime: adapter %s supplied unsupported registration %T", address, implementation)
		}
		registration = *pointer
	}
	if registration.ContractRevision != registry.contract {
		return fmt.Errorf("runtime: adapter %s contract_revision mismatch", address)
	}
	if strings.TrimSpace(registration.PackageContractABIRevision) == "" {
		return fmt.Errorf("runtime: adapter %s has no package_contract_abi_revision", address)
	}
	if registration.RuntimeABI != ContractRuntimeABI {
		return fmt.Errorf("runtime: adapter %s requires unsupported runtime ABI %q", address, registration.RuntimeABI)
	}
	for provider, requiredABI := range registration.ProviderABIs {
		if strings.TrimSpace(provider) == "" || strings.TrimSpace(requiredABI) == "" {
			return fmt.Errorf("runtime: adapter %s has an invalid provider ABI requirement", address)
		}
		if available := registry.providerABIs[provider]; available != requiredABI {
			return fmt.Errorf("runtime: adapter %s requires provider %s ABI %q; runtime has %q", address, provider, requiredABI, available)
		}
	}
	if registration.Apply == nil {
		return fmt.Errorf("runtime: adapter %s has no registration function", address)
	}
	covered := canonicalContractAddresses(registration.CoveredAddresses)
	if len(covered) == 0 {
		return fmt.Errorf("runtime: adapter %s covers no resources", address)
	}
	for _, resourceAddress := range covered {
		if owner, exists := registry.owners[resourceAddress]; exists {
			return fmt.Errorf("runtime: resource %s is owned by both %s and %s", resourceAddress, owner, address)
		}
		if len(registry.required) > 0 && !registry.required[resourceAddress] {
			return fmt.Errorf("runtime: adapter %s claims unexpected resource %s", address, resourceAddress)
		}
		registry.owners[resourceAddress] = address
	}
	registration.CoveredAddresses = covered
	registry.registrations[address] = registration
	return nil
}

func (registry *ContractRegistry) Seal() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return fmt.Errorf("runtime: contract registry is already sealed")
	}
	for address := range registry.required {
		if registry.owners[address] == "" {
			return fmt.Errorf("runtime: required contract resource %s has no application adapter", address)
		}
	}
	addresses := make([]string, 0, len(registry.registrations))
	for address := range registry.registrations {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	snapshot := snapshotContractRuntimeState()
	for _, address := range addresses {
		if err := registry.registrations[address].Apply(); err != nil {
			restoreContractRuntimeState(snapshot)
			return fmt.Errorf("runtime: register application adapter %s: %w", address, err)
		}
	}
	if err := validateContractPagesRegistered(); err != nil {
		restoreContractRuntimeState(snapshot)
		return fmt.Errorf("runtime: validate contract pages: %w", err)
	}
	registry.sealed = true
	return nil
}

func cloneContractStringMap(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

type contractRuntimeSnapshot struct {
	endpoints                 map[string]*Endpoint
	cronJobs                  map[string]*CronJob
	durableTasks              map[string]*DurableTask
	contractDurableExecutions map[string]ContractDurableRegistration
	contractBindings          map[string]ContractInternalBindingRegistration
	contractCLIBindings       map[string]ContractCLIBindingRegistration
	contractPages             map[string]ContractPageRegistration
	contractEventBuses        map[string]ContractEventBus
	contractEventConsumers    map[string]ContractEventConsumerRegistration
	contractEventEmissions    map[string]ContractEventEmissionRegistration
	serviceInitializers       map[string]serviceInitializer
	serviceInitOrder          map[string]int
	serviceShutdowns          map[string]serviceShutdown
}

func snapshotContractRuntimeState() contractRuntimeSnapshot {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return contractRuntimeSnapshot{
		endpoints: cloneContractMap(global.endpoints), cronJobs: cloneContractMap(global.cronJobs), durableTasks: cloneContractMap(global.durableTasks),
		contractDurableExecutions: cloneContractMap(global.contractDurableExecutions), contractBindings: cloneContractMap(global.contractBindings),
		contractCLIBindings: cloneContractMap(global.contractCLIBindings),
		contractPages:       cloneContractMap(global.contractPages),
		contractEventBuses:  cloneContractMap(global.contractEventBuses), contractEventConsumers: cloneContractMap(global.contractEventConsumers),
		contractEventEmissions: cloneContractMap(global.contractEventEmissions), serviceInitializers: cloneContractMap(global.serviceInitializers),
		serviceInitOrder: cloneContractMap(global.serviceInitOrder), serviceShutdowns: cloneContractMap(global.serviceShutdowns),
	}
}

func restoreContractRuntimeState(snapshot contractRuntimeSnapshot) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.endpoints = snapshot.endpoints
	global.cronJobs = snapshot.cronJobs
	global.durableTasks = snapshot.durableTasks
	global.contractDurableExecutions = snapshot.contractDurableExecutions
	global.contractBindings = snapshot.contractBindings
	global.contractCLIBindings = snapshot.contractCLIBindings
	global.contractPages = snapshot.contractPages
	global.contractEventBuses = snapshot.contractEventBuses
	global.contractEventConsumers = snapshot.contractEventConsumers
	global.contractEventEmissions = snapshot.contractEventEmissions
	global.serviceInitializers = snapshot.serviceInitializers
	global.serviceInitOrder = snapshot.serviceInitOrder
	global.serviceShutdowns = snapshot.serviceShutdowns
}

func cloneContractMap[K comparable, V any](values map[K]V) map[K]V {
	clone := make(map[K]V, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func canonicalContractAddresses(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
