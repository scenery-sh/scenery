package runtime

import "testing"

func TestContractProviderABIsComeFromRuntimeRegistration(t *testing.T) {
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
