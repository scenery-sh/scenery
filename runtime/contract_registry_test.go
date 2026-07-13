package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestContractRegistryValidatesCompleteOwnershipBeforeApplying(t *testing.T) {
	registry, err := NewContractRegistry(ContractRegistryOptions{ContractRevision: "sha256:contract", RequiredAddresses: []string{"house/service/house", "house/operation/process"}})
	if err != nil {
		t.Fatal(err)
	}
	var applied []string
	if err := registry.Register("adapter/house", ContractRegistration{
		ContractRevision: "sha256:contract", PackageContractABIRevision: "sha256:package", RuntimeABI: ContractRuntimeABI,
		CoveredAddresses: []string{"house/operation/process", "house/service/house"},
		Apply:            func() error { applied = append(applied, "house"); return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("registration applied before seal: %v", applied)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, []string{"house"}) {
		t.Fatalf("applied = %v", applied)
	}
	if err := registry.Register("adapter/late", ContractRegistration{}); err == nil {
		t.Fatal("late registration succeeded")
	}
}

func TestContractRegistryRejectsMismatchDuplicateAndIncompleteSet(t *testing.T) {
	registry, err := NewContractRegistry(ContractRegistryOptions{ContractRevision: "sha256:contract", RequiredAddresses: []string{"house/service/house", "house/operation/process"}})
	if err != nil {
		t.Fatal(err)
	}
	registration := ContractRegistration{ContractRevision: "sha256:wrong", PackageContractABIRevision: "sha256:package", RuntimeABI: ContractRuntimeABI, CoveredAddresses: []string{"house/service/house"}, Apply: func() error { return nil }}
	if err := registry.Register("adapter/house", registration); err == nil {
		t.Fatal("contract mismatch succeeded")
	}
	registration.ContractRevision = "sha256:contract"
	if err := registry.Register("adapter/house", registration); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("adapter/other", registration); err == nil {
		t.Fatal("duplicate ownership succeeded")
	}
	if err := registry.Seal(); err == nil {
		t.Fatal("incomplete ownership sealed")
	}
}

func TestContractRegistryPropagatesAdapterFailureWithoutSealing(t *testing.T) {
	registry, err := NewContractRegistry(ContractRegistryOptions{ContractRevision: "sha256:contract", RequiredAddresses: []string{"house/service/house"}})
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := registry.Register("adapter/house", ContractRegistration{ContractRevision: "sha256:contract", PackageContractABIRevision: "sha256:package", RuntimeABI: ContractRuntimeABI, CoveredAddresses: []string{"house/service/house"}, Apply: func() error { return boom }}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); !errors.Is(err, boom) {
		t.Fatalf("seal error = %v", err)
	}
}

func TestContractRegistryRejectsProviderABIMismatch(t *testing.T) {
	registry, err := NewContractRegistry(ContractRegistryOptions{ContractRevision: "sha256:contract", RequiredAddresses: []string{"house/service/house"}, ProviderABIs: map[string]string{"registry.scenery.dev/core/postgres": "scenery.data-runtime/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	err = registry.Register("adapter/house", ContractRegistration{
		ContractRevision: "sha256:contract", PackageContractABIRevision: "sha256:package", RuntimeABI: ContractRuntimeABI,
		ProviderABIs: map[string]string{"registry.scenery.dev/core/postgres": "scenery.data-runtime/" + "v2"}, CoveredAddresses: []string{"house/service/house"}, Apply: func() error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "requires provider") {
		t.Fatalf("provider ABI mismatch error = %v", err)
	}
}

func TestContractRegistrySealRollsBackRuntimeRegistrations(t *testing.T) {
	registry, err := NewContractRegistry(ContractRegistryOptions{ContractRevision: "sha256:contract", RequiredAddresses: []string{"house/service/one", "house/service/two"}})
	if err != nil {
		t.Fatal(err)
	}
	binding := "test/contract-registry/atomic-binding"
	if err := registry.Register("adapter/one", ContractRegistration{
		ContractRevision: "sha256:contract", PackageContractABIRevision: "sha256:one", RuntimeABI: ContractRuntimeABI,
		CoveredAddresses: []string{"house/service/one"}, Apply: func() error {
			return RegisterContractInternalBinding(binding, func(context.Context, any, any) (any, error) { return nil, nil })
		},
	}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := registry.Register("adapter/two", ContractRegistration{
		ContractRevision: "sha256:contract", PackageContractABIRevision: "sha256:two", RuntimeABI: ContractRuntimeABI,
		CoveredAddresses: []string{"house/service/two"}, Apply: func() error { return boom },
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); !errors.Is(err, boom) {
		t.Fatalf("seal error = %v", err)
	}
	if err := RegisterContractInternalBinding(binding, func(context.Context, any, any) (any, error) { return nil, nil }); err != nil {
		t.Fatalf("failed Seal leaked binding registration: %v", err)
	}
	global.mu.Lock()
	delete(global.contractBindings, binding)
	global.mu.Unlock()
}
