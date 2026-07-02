package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/sqlitedb"
)

func TestMetadataWithRuntimeSQLiteDatabasesIncludesFileSizes(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "testapp",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"db": {Kind: "sqlite", Database: "primary"},
		}},
	}
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{
		AppRoot: root,
		Config:  cfg,
		Mode:    sqlitedb.ModeLocal,
	})
	if err != nil {
		t.Fatalf("ResolveServices returned error: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("ResolveServices returned %d services, want 1", len(services))
	}
	if err := os.MkdirAll(filepath.Dir(services[0].Path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(services[0].Path, []byte("sqlite bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	supervisor := &devSupervisor{root: root, cfg: cfg}
	got := supervisor.metadataWithRuntimeSQLiteDatabases(json.RawMessage(`{"module_path":"example.test/app"}`), root, "")
	var payload struct {
		SQLDatabases []struct {
			Name      string `json:"name"`
			FileLabel string `json:"file_label"`
			Path      string `json:"path"`
			SizeBytes int64  `json:"size_bytes"`
			Exists    bool   `json:"exists"`
		} `json:"sql_databases"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(payload.SQLDatabases) != 1 {
		t.Fatalf("sql_databases length = %d, want 1 in %s", len(payload.SQLDatabases), got)
	}
	db := payload.SQLDatabases[0]
	if db.Name != "db" || db.FileLabel != "primary" || db.Path != services[0].Path {
		t.Fatalf("database metadata = %+v, want name db, file label primary, path %s", db, services[0].Path)
	}
	if db.SizeBytes != int64(len("sqlite bytes")) || !db.Exists {
		t.Fatalf("database size/existence = %d/%v, want %d/true", db.SizeBytes, db.Exists, len("sqlite bytes"))
	}
}

func TestMetadataWithRuntimeSQLiteDatabasesUsesSessionPath(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "testapp",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"db": {Kind: "sqlite"},
		}},
	}
	sessionID := "session-123"
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{
		AppRoot:   root,
		Config:    cfg,
		Mode:      sqlitedb.ModeSession,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("ResolveServices returned error: %v", err)
	}
	if err := sqlitedb.EnsureFiles(context.Background(), services); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	supervisor := &devSupervisor{root: root, cfg: cfg}
	got := supervisor.metadataWithRuntimeSQLiteDatabases(json.RawMessage(`{}`), root, sessionID)
	var payload struct {
		SQLDatabases []struct {
			Path string `json:"path"`
		} `json:"sql_databases"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(payload.SQLDatabases) != 1 || payload.SQLDatabases[0].Path != services[0].Path {
		t.Fatalf("sql_databases = %+v, want path %s", payload.SQLDatabases, services[0].Path)
	}
}

func TestMetadataWithRuntimeSQLiteDatabasesDiscoversCurrentSessionServiceDatabases(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{Name: "testapp"}
	sessionID := "session-123"
	paths := []string{
		filepath.Join(root, ".scenery", "sessions", sessionID, "sqlite", "tasks.sqlite"),
		filepath.Join(root, ".scenery", "sessions", "old-session", "sqlite", "old.sqlite"),
		filepath.Join(root, ".scenery", "state", "db", "jobs.durable.sqlite"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte("runtime"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	got := metadataWithRuntimeSQLiteDatabases(json.RawMessage(`{}`), root, sessionID, cfg, true)
	var payload struct {
		SQLDatabases []struct {
			Name      string `json:"name"`
			Path      string `json:"path"`
			SizeBytes int64  `json:"size_bytes"`
			Exists    bool   `json:"exists"`
		} `json:"sql_databases"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(payload.SQLDatabases) != 1 {
		t.Fatalf("sql_databases length = %d, want 1 in %s", len(payload.SQLDatabases), got)
	}
	if payload.SQLDatabases[0].Name != "tasks" || payload.SQLDatabases[0].Path != paths[0] {
		t.Fatalf("runtime database = %+v, want current session tasks database", payload.SQLDatabases[0])
	}
	if payload.SQLDatabases[0].SizeBytes != int64(len("runtime")) || !payload.SQLDatabases[0].Exists {
		t.Fatalf("runtime database = %+v, want size/existence", payload.SQLDatabases[0])
	}
}
