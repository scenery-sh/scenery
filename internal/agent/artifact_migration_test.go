package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryMigrationPreservesSessionAndSubstrateOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	legacy := []byte(`{"sessions":[{"schema_version":"scenery.dev.session.v1","session_id":"onlv-main","app_root":"/repo/onlv","state_root":"/repo/onlv/.scenery/state/onlv-main","status":"ready","owner_pid":42,"owner":{"pid":42,"recorded_at":"2026-07-13T00:00:00Z"},"route_manifest":{"schema_version":"scenery.route_manifest.v1","mode":"path","port_lease":{"schema_version":"scenery.dev.port_lease.v1","app_root":"/repo/onlv","session_id":"onlv-main","port":4001,"url":"http://localhost:4001","owner":{"pid":42,"recorded_at":"2026-07-13T00:00:00Z"},"created_at":"2026-07-13T00:00:00Z","updated_at":"2026-07-13T00:00:00Z"}},"backends":{},"created_at":"2026-07-13T00:00:00Z","updated_at":"2026-07-13T00:00:00Z"}],"substrates":[{"schema_version":"scenery.dev.substrate.v1","kind":"postgres","status":"ready","owner_pid":99,"owner":{"pid":99,"recorded_at":"2026-07-13T00:00:00Z"},"created_at":"2026-07-13T00:00:00Z","updated_at":"2026-07-13T00:00:00Z"}]}`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	registry, err := OpenRegistry(path, "127.0.0.1:4040")
	if err != nil {
		t.Fatal(err)
	}
	if registry.sessions["onlv-main"].OwnerPID != 42 || registry.substrates["postgres"].OwnerPID != 99 {
		t.Fatalf("ownership lost: sessions=%+v substrates=%+v", registry.sessions, registry.substrates)
	}
	if registry.sessions["onlv-main"].Kind != SessionKind || registry.substrates["postgres"].ArtifactIdentity.Kind != SubstrateKind {
		t.Fatalf("identities not migrated: %+v %+v", registry.sessions["onlv-main"].ArtifactIdentity, registry.substrates["postgres"].ArtifactIdentity)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if _, err := OpenRegistry(path, "127.0.0.1:4040"); err != nil {
		t.Fatalf("idempotent reload: %v", err)
	}
}

func TestEdgeTargetMigrationPreservesProcessOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edge-target.json")
	legacy := []byte(`{"schema_version":"scenery.edge.target.v1","kind":"caddy","target_addr":"127.0.0.1:19443","pid":123,"owner_uid":501,"owner_gid":20,"process_start":"start","executable":"/usr/local/bin/caddy","updated_at":"2026-07-13T00:00:00Z"}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := LoadEdgeTargetState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != EdgeKindCaddy || state.PID != 123 || state.OwnerUID != 501 || state.ArtifactIdentity.Kind != EdgeTargetKind {
		t.Fatalf("migrated state = %+v", state)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}

func TestAgentStateMigrationPreservesRunningIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := []byte(`{"schema_version":"scenery.agent.state.v1","pid":123,"version":"v1.2.3","commit":"abc","socket_path":"/tmp/agent.sock","router_addr":"127.0.0.1:4040","updated_at":"2026-07-13T00:00:00Z"}`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.PID != 123 || state.Identity.Version != "v1.2.3" || state.Identity.Commit != "abc" || state.Kind != AgentStateKind {
		t.Fatalf("migrated state = %+v", state)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}
