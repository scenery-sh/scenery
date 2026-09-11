package nativecompose

import (
	"errors"
	"testing"
)

func registration(addresses ...string) Registration {
	return Registration{ContractRevision: "contract", PackageContractABIRevision: "package", RuntimeABI: RuntimeABI, CoveredAddresses: addresses, Apply: func() error { return nil }}
}

func TestRejectedAdapterDoesNotReserveResources(t *testing.T) {
	registry, err := New(Options{ContractRevision: "contract", RequiredAddresses: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("bad", registration("a", "unexpected")); err == nil {
		t.Fatal("unexpected resource accepted")
	}
	if err := registry.Register("a", registration("a")); err != nil {
		t.Fatalf("rejected adapter reserved a resource: %v", err)
	}
	began := false
	begin := func() Transaction {
		began = true
		return Transaction{Validate: func() error { return nil }, Rollback: func() {}}
	}
	if err := registry.Seal(begin); err == nil || began {
		t.Fatal("incomplete composition began applying")
	}
	if err := registry.Register("b", registration("b")); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(begin); err != nil || !began {
		t.Fatalf("complete composition = %v", err)
	}
}

func TestFailedApplyRestoresNativeRegistrationState(t *testing.T) {
	for _, phase := range []string{"apply", "validate"} {
		t.Run(phase, func(t *testing.T) {
			registry, err := New(Options{ContractRevision: "contract", RequiredAddresses: []string{"resource"}})
			if err != nil {
				t.Fatal(err)
			}
			original := &struct{ ID int }{1}
			candidate := &struct{ ID int }{2}
			state := original
			failure := errors.New("rejected")
			fail := true
			adapter := registration("resource")
			adapter.Apply = func() error {
				state = candidate
				if fail && phase == "apply" {
					return failure
				}
				return nil
			}
			if err := registry.Register("adapter", adapter); err != nil {
				t.Fatal(err)
			}
			rollbacks := 0
			begin := func() Transaction {
				previous := state
				return Transaction{
					Validate: func() error {
						if fail && phase == "validate" {
							return failure
						}
						return nil
					},
					Rollback: func() { rollbacks++; state = previous },
				}
			}
			if err := registry.Seal(begin); !errors.Is(err, failure) || state != original || rollbacks != 1 {
				t.Fatalf("failed composition error=%v state=%v rollbacks=%d", err, state, rollbacks)
			}
			fail = false
			if err := registry.Seal(begin); err != nil || state != candidate || rollbacks != 1 {
				t.Fatalf("retry error=%v state=%v rollbacks=%d", err, state, rollbacks)
			}
		})
	}
}
