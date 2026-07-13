package main

import (
	"encoding/json"
	"testing"
)

func TestHarnessDetachStateRootReadsCLIEnvelope(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newCLIEnvelope(true, map[string]any{"kind": "scenery.dev.detach", "schema_revision": "sha256:test", "session": map[string]any{"state_root": "/tmp/state"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := harnessDetachStateRoot(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/state" {
		t.Fatalf("state root = %q, want /tmp/state", got)
	}
}
