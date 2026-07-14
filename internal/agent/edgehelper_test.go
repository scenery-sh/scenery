package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEdgeHelperTargetReadsCurrentWriterOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edge-target.json")
	if err := WriteEdgeTargetState(path, EdgeTargetState{
		Kind:           EdgeKindCaddy,
		TargetAddr:     "127.0.0.1:19443",
		HTTPTargetAddr: "127.0.0.1:19080",
		PID:            4242,
		OwnerUID:       501,
		OwnerGID:       20,
		ProcessStart:   "12345",
		Executable:     "/tmp/toolchain/caddy",
	}); err != nil {
		t.Fatalf("WriteEdgeTargetState: %v", err)
	}
	target, err := LoadEdgeHelperTarget(path)
	if err != nil {
		t.Fatalf("LoadEdgeHelperTarget: %v", err)
	}
	want := EdgeHelperTarget{
		Kind:           EdgeKindCaddy,
		TargetAddr:     "127.0.0.1:19443",
		HTTPTargetAddr: "127.0.0.1:19080",
		PID:            4242,
		OwnerUID:       501,
		OwnerGID:       20,
		ProcessStart:   "12345",
		Executable:     "/tmp/toolchain/caddy",
	}
	if target != want {
		t.Fatalf("LoadEdgeHelperTarget = %+v, want %+v", target, want)
	}
}

func TestLoadEdgeHelperTargetToleratesFutureRevisionsAndNeverRewrites(t *testing.T) {
	// Metadata written by a hypothetical future scenery: unknown identity
	// revisions, a new payload field, and unknown identity metadata. An
	// installed helper must keep forwarding without being able to decode the
	// full current artifact, and must not rewrite the user-owned file.
	future := []byte(`{
  "kind": "scenery.edge.target",
  "schema_revision": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
  "spec_revision": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "producer": {"version": "v99.0.0", "toolchain": {"go_version": "go2.0"}},
  "edge_kind": "caddy",
  "target_addr": " 127.0.0.1:19443 ",
  "http_target_addr": "127.0.0.1:19080",
  "quic_target_addr": "127.0.0.1:19444",
  "pid": 777,
  "owner_uid": 501,
  "owner_gid": 20,
  "process_start": "98765",
  "executable": "/tmp/toolchain/caddy",
  "updated_at": "2099-01-01T00:00:00Z"
}
`)
	path := filepath.Join(t.TempDir(), "edge-target.json")
	if err := os.WriteFile(path, future, 0o600); err != nil {
		t.Fatal(err)
	}
	// The strict durable loader must reject this future artifact; the frozen
	// helper handoff reader must accept it.
	if _, err := LoadEdgeTargetState(path); err == nil {
		t.Fatalf("LoadEdgeTargetState accepted future revisions; the helper regression this guards against no longer reproduces")
	}
	target, err := LoadEdgeHelperTarget(path)
	if err != nil {
		t.Fatalf("LoadEdgeHelperTarget: %v", err)
	}
	if target.Kind != EdgeKindCaddy || target.TargetAddr != "127.0.0.1:19443" || target.HTTPTargetAddr != "127.0.0.1:19080" || target.PID != 777 {
		t.Fatalf("LoadEdgeHelperTarget = %+v", target)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(future) {
		t.Fatalf("helper reader rewrote target metadata:\n%s", after)
	}
	if _, err := os.Stat(path + ".legacy.migrated"); err == nil {
		t.Fatalf("helper reader left a migration marker next to target metadata")
	}
}

func TestLoadEdgeHelperTargetRejectsMissingAndMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadEdgeHelperTarget(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatalf("LoadEdgeHelperTarget accepted a missing file")
	}
	path := filepath.Join(dir, "edge-target.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEdgeHelperTarget(path); err == nil {
		t.Fatalf("LoadEdgeHelperTarget accepted malformed JSON")
	}
}
