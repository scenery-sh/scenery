// Package nativecompose validates and applies one complete native composition.
// The runtime supplies transaction ownership; callbacks retain their Go values.
package nativecompose

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
)

// Transaction restores registrations after a failed apply or final validation.
// Begin is called only once the complete ABI and resource set is admitted.
type Transaction struct {
	Validate func() error
	Rollback func()
}

const RuntimeABI = "scenery.go-runtime/v1"

// Registration is the application-adapter payload accepted by the
// contract registration boundary. Apply is delayed until Seal has verified
// the complete ownership set, so missing or duplicate adapters cannot expose a
// partial runtime graph.
type Registration struct {
	ContractRevision           string
	PackageContractABIRevision string
	RuntimeABI                 string
	ProviderABIs               map[string]string
	CoveredAddresses           []string
	Apply                      func() error
}

type Options struct {
	ContractRevision  string
	RequiredAddresses []string
	ProviderABIs      map[string]string
}

type Registry struct {
	mu            sync.Mutex
	contract      string
	required      map[string]bool
	owners        map[string]string
	registrations map[string]Registration
	providerABIs  map[string]string
	sealed        bool
}

func New(options Options) (*Registry, error) {
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
	return &Registry{
		contract: options.ContractRevision, required: required,
		owners: map[string]string{}, registrations: map[string]Registration{}, providerABIs: maps.Clone(options.ProviderABIs),
	}, nil
}

func (registry *Registry) Register(address string, implementation any) error {
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
	registration, ok := implementation.(Registration)
	if !ok {
		pointer, pointerOK := implementation.(*Registration)
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
	if registration.RuntimeABI != RuntimeABI {
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
	covered := canonicalAddresses(registration.CoveredAddresses)
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
	}
	for _, resourceAddress := range covered {
		registry.owners[resourceAddress] = address
	}
	registration.CoveredAddresses = covered
	registry.registrations[address] = registration
	return nil
}

func (registry *Registry) Seal(begin func() Transaction) error {
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
	if begin == nil {
		return fmt.Errorf("runtime: contract registry requires a transaction owner")
	}
	transaction := begin()
	if transaction.Rollback == nil || transaction.Validate == nil {
		return fmt.Errorf("runtime: incomplete contract registration transaction")
	}
	for _, address := range addresses {
		if err := registry.registrations[address].Apply(); err != nil {
			transaction.Rollback()
			return fmt.Errorf("runtime: register application adapter %s: %w", address, err)
		}
	}
	if err := transaction.Validate(); err != nil {
		transaction.Rollback()
		return err
	}
	registry.sealed = true
	return nil
}

func canonicalAddresses(values []string) []string {
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
