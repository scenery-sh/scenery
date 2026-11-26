package auth

import (
	"sync"
	"testing"

	authdb "scenery.sh/auth/db/gen"
)

func resetStandardAuthStateForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		standardAuthState.mu.Lock()
		if standardAuthState.svc != nil && standardAuthState.svc.db != nil {
			_ = standardAuthState.svc.db.Close()
		}
		standardAuthState.enabled = false
		standardAuthState.svc = nil
		standardAuthState.once = sync.Once{}
		standardAuthState.err = nil
		standardAuthState.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func mustParseAuthUUID(t *testing.T, value string) authdb.UUID {
	t.Helper()
	id, err := parseUUID(value)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", value, err)
	}
	return id
}
