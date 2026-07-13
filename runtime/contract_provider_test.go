package runtime

import "testing"

func TestContractProviderABIsComeFromRuntimeRegistration(t *testing.T) {
	builtins := map[string]string{
		"registry.scenery.dev/core/postgres": "scenery.data-runtime/v1",
		"registry.scenery.dev/core/storage":  "scenery.object/v1",
		"registry.scenery.dev/core/durable":  "scenery.execution-runtime/v1",
	}
	for identity, want := range builtins {
		if got := ContractProviderABIs()[identity]; got != want {
			t.Fatalf("built-in provider %s ABI = %q, want %q", identity, got, want)
		}
	}

	identity := "test.provider/runtime@1.0.0"
	if err := RegisterContractProviderABI(identity, "test.runtime/v1"); err != nil {
		t.Fatal(err)
	}
	snapshot := ContractProviderABIs()
	if snapshot[identity] != "test.runtime/v1" {
		t.Fatalf("provider snapshot = %#v", snapshot)
	}
	snapshot[identity] = "tampered"
	if ContractProviderABIs()[identity] != "test.runtime/v1" {
		t.Fatal("provider ABI snapshot mutated runtime registration")
	}
	if err := RegisterContractProviderABI(identity, "test.runtime/v2"); err == nil {
		t.Fatal("conflicting provider ABI registration succeeded")
	}
}

func TestContractRegistryAcceptsBuiltinProviderABIsAndRejectsMismatch(t *testing.T) {
	const address = "service/service/agents"
	registration := ContractRegistration{
		ContractRevision: "sha256:contract", PackageContractABIRevision: "sha256:package", RuntimeABI: ContractRuntimeABI,
		ProviderABIs: map[string]string{
			"registry.scenery.dev/core/postgres": "scenery.data-runtime/v1",
			"registry.scenery.dev/core/storage":  "scenery.object/v1",
			"registry.scenery.dev/core/durable":  "scenery.execution-runtime/v1",
		},
		CoveredAddresses: []string{address}, Apply: func() error { return nil },
	}
	registry, err := NewContractRegistry(ContractRegistryOptions{
		ContractRevision: "sha256:contract", RequiredAddresses: []string{address}, ProviderABIs: ContractProviderABIs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("agents/service/agents/adapter", registration); err != nil {
		t.Fatal(err)
	}

	registration.ProviderABIs["registry.scenery.dev/core/storage"] = "scenery.object/" + "v2"
	mismatch, err := NewContractRegistry(ContractRegistryOptions{
		ContractRevision: "sha256:contract", RequiredAddresses: []string{address}, ProviderABIs: ContractProviderABIs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mismatch.Register("agents/service/agents/adapter", registration); err == nil {
		t.Fatal("provider ABI mismatch succeeded")
	}
}
