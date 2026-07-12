package main

import "testing"

func TestSystemTrustDispatchesToEdgeTrust(t *testing.T) {
	previous := systemEdgeTrustFunc
	t.Cleanup(func() { systemEdgeTrustFunc = previous })
	called := false
	systemEdgeTrustFunc = func(opts edgeOptions) error {
		called = true
		if !opts.JSON {
			t.Fatal("edge trust options did not preserve -o json")
		}
		return nil
	}

	if err := systemCommand([]string{"trust", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("system trust did not dispatch to edge trust")
	}
}
