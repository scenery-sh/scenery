package agent

import (
	"path/filepath"
	"testing"
)

func TestDeleteSessionPrunesOnlyMatchingSubstrateLease(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	session, err := registry.Upsert(RegisterRequest{BaseAppID: "demo", AppRoot: root, SessionID: "stale", Status: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:   SubstratePostgres,
		Status: "ready",
		Leases: map[string]SubstrateLease{
			session.SessionID: {SessionID: session.SessionID, AppRoot: root},
			"other":           {SessionID: "other", AppRoot: "/tmp/other"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := registry.Delete(session.SessionID); err != nil || !deleted {
		t.Fatalf("delete session: deleted=%v err=%v", deleted, err)
	}
	substrate, ok := registry.GetSubstrate(SubstratePostgres)
	if !ok {
		t.Fatal("shared substrate was deleted")
	}
	if _, ok := substrate.Leases[session.SessionID]; ok {
		t.Fatalf("deleted session lease remains: %+v", substrate.Leases)
	}
	if _, ok := substrate.Leases["other"]; !ok {
		t.Fatalf("unrelated lease was removed: %+v", substrate.Leases)
	}
}
