package runtime

import "testing"

func TestContractProviderABIsComeFromRuntimeRegistration(t *testing.T) {
	builtins := map[string]string{
		"registry.scenery.dev/core/postgres@2.1.0": "scenery.data-runtime/v1",
		"registry.scenery.dev/core/storage@2.0.0":  "scenery.object/v1",
		"registry.scenery.dev/core/durable@1.0.0":  "scenery.execution-runtime/v1",
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
