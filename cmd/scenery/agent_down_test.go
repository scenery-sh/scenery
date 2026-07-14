package main

import (
	"context"
	"strings"
	"testing"
)

func TestResolveDownSessionAllowsMissingRuntime(t *testing.T) {
	t.Parallel()

	session, missing, err := resolveDownSessionFromList("/tmp/app", nil, downOptions{DB: true})
	if err != nil {
		t.Fatalf("resolve down session with db cleanup: %v", err)
	}
	if !missing || session.AppRoot != "/tmp/app" {
		t.Fatalf("session = %+v missing=%v, want synthetic missing-runtime session", session, missing)
	}
	session, missing, err = resolveDownSessionFromList("/tmp/app", nil, downOptions{})
	if err != nil || !missing || session.AppRoot != "/tmp/app" {
		t.Fatalf("resolve down session without cleanup = %+v missing=%v err=%v", session, missing, err)
	}
}

func TestDropSessionManagedDatabaseRefusesExternalDSN(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"demo","dev":{"services":{"main":{}}}}`)
	writeTestAppFile(t, root, ".env", "DATABASE_URL=postgres://user:secret@127.0.0.1:5432/demo\n")

	_, err := dropSessionManagedDatabase(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "refusing to drop external postgres database") {
		t.Fatalf("drop managed database error = %v, want external database refusal", err)
	}
}
