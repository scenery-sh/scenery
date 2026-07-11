package runtime

import (
	"fmt"
	"strings"
	"sync"
)

var contractProviders = struct {
	sync.RWMutex
	abis map[string]string
}{abis: map[string]string{}}

// RegisterContractProviderABI is called by a loaded provider runtime to attest
// the exact ABI it actually implements. Generated application requirements do
// not populate this registry.
func RegisterContractProviderABI(identity, abi string) error {
	identity, abi = strings.TrimSpace(identity), strings.TrimSpace(abi)
	if identity == "" || abi == "" {
		return fmt.Errorf("runtime: provider identity and ABI are required")
	}
	contractProviders.Lock()
	defer contractProviders.Unlock()
	if existing := contractProviders.abis[identity]; existing != "" && existing != abi {
		return fmt.Errorf("runtime: provider %s registered conflicting ABIs %q and %q", identity, existing, abi)
	}
	contractProviders.abis[identity] = abi
	return nil
}

// ContractProviderABIs returns an immutable snapshot of loaded provider
// runtime attestations for the application registration boundary.
func ContractProviderABIs() map[string]string {
	contractProviders.RLock()
	defer contractProviders.RUnlock()
	return cloneContractStringMap(contractProviders.abis)
}

func init() {
	// These implementations are linked into scenery/runtime itself.
	_ = RegisterContractProviderABI("registry.scenery.dev/core/postgres@2.1.0", "scenery.data-runtime/v1")
	_ = RegisterContractProviderABI("registry.scenery.dev/core/storage@2.0.0", "scenery.object/v1")
	_ = RegisterContractProviderABI("registry.scenery.dev/core/durable@1.0.0", "scenery.execution-runtime/v1")
}
