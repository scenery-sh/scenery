package sqlitedb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestResolveServicesAndEnv(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
		"auth":    {Kind: "sqlite"},
		"billing": {Kind: "sqlite", Database: "billing-data", DatabaseURLEnv: "BILLING_DB"},
	}}}
	services, err := ResolveServices(ResolveRequest{AppRoot: root, Config: cfg, SessionID: "s1", Mode: ModeSession})
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("service count = %d, want 2", len(services))
	}
	auth := services[0]
	if auth.Name != "auth" || auth.DatabaseURLEnv != "AUTH_DATABASE_URL" || auth.DatabasePathEnv != "AUTH_DATABASE_PATH" {
		t.Fatalf("auth service = %+v", auth)
	}
	if !strings.Contains(auth.Path, filepath.Join(".scenery", "sessions", "s1", "sqlite", "auth.sqlite")) {
		t.Fatalf("auth path = %q", auth.Path)
	}
	env := strings.Join(Env(services[:1], true), "\n")
	if !strings.Contains(env, "DatabaseURL=sqlite://") || !strings.Contains(env, "SCENERY_SQLITE_DATABASES_JSON=") {
		t.Fatalf("env = %s", env)
	}
}

func TestEnsureFilesAndBackup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := app.Config{Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
		"auth": {Kind: "sqlite"},
	}}}
	services, err := ResolveServices(ResolveRequest{AppRoot: root, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureFiles(ctx, services); err != nil {
		t.Fatalf("EnsureFiles: %v", err)
	}
	db, err := Open(ctx, services[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE demo (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO demo (name) VALUES ('ok')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "snapshot.sqlite")
	if err := Backup(ctx, services[0].Path, target); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	schema, err := DumpSchema(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "CREATE TABLE demo") {
		t.Fatalf("schema = %s", schema)
	}
}

func TestParseURL(t *testing.T) {
	got, err := ParseURL("sqlite:///tmp/demo.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/demo.sqlite" {
		t.Fatalf("path = %q", got)
	}
	if _, err := ParseURL("mysql://localhost/db"); err == nil {
		t.Fatalf("expected non-sqlite URL to fail")
	}
}
