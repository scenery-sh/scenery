package authbridge

import "testing"

// Embedding supplies unused methods; this test exercises registration ownership.
type testRuntime struct{ Runtime }

func TestRuntimeRegistrationBeforeAndAfterBinding(t *testing.T) {
	var owner runtimeOwner
	host := &testRuntime{}
	calls := 0
	register := func(got Runtime) {
		if got != host {
			t.Fatal("registration received a different host")
		}
		calls++
	}
	owner.whenReady(register)
	if calls != 0 {
		t.Fatal("registration ran before host initialization")
	}
	owner.bind(host)
	owner.whenReady(register)
	if calls != 2 || len(owner.pending) != 0 {
		t.Fatalf("registrations=%d pending=%d", calls, len(owner.pending))
	}
	defer func() {
		if recover() == nil {
			t.Fatal("second host accepted")
		}
	}()
	owner.bind(host)
}
