package main

import "testing"

func TestHarnessDetachStateRootReadsCLIEnvelope(t *testing.T) {
	t.Parallel()

	got, err := harnessDetachStateRoot(`{"api_version":"scenery.cli.v1","ok":true,"data":{"schema_version":"scenery.dev.detach.v1","session":{"state_root":"/tmp/state"}},"diagnostics":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/state" {
		t.Fatalf("state root = %q, want /tmp/state", got)
	}
}
