package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/sqlitedb"
)

func TestDashboardSQLiteReadOnlyRPCs(t *testing.T) {
	server := newTestDashboardServer(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"sqlite-test"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	dbPath := filepath.Join(root, ".scenery", "sessions", "session-a", "sqlite", "tasks.sqlite")
	db, err := sqlitedb.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE tasks(id INTEGER PRIMARY KEY, title TEXT); INSERT INTO tasks(title) VALUES ('one'), ('two')`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	if err := server.supervisor.store.UpsertApp(context.Background(), devdash.AppRecord{
		ID:        "app-test",
		BaseAppID: "app-test",
		SessionID: "session-a",
		Name:      "app-test",
		Root:      root,
		Running:   true,
	}); err != nil {
		t.Fatalf("upsert app: %v", err)
	}

	params, _ := json.Marshal(map[string]any{"app_id": "app-test", "database": "tasks"})
	tables, err := server.dispatchRPC(context.Background(), "sqlite/tables", params)
	if err != nil {
		t.Fatalf("sqlite/tables: %v", err)
	}
	if got := tables.([]dashboardSQLiteTable); len(got) != 2 || got[0].Name != "scenery_sqlite_metadata" || got[1].Name != "tasks" {
		t.Fatalf("tables = %+v", got)
	}

	params, _ = json.Marshal(map[string]any{"app_id": "app-test", "database": "tasks", "table": "tasks"})
	schema, err := server.dispatchRPC(context.Background(), "sqlite/schema", params)
	if err != nil {
		t.Fatalf("sqlite/schema: %v", err)
	}
	if got := schema.([]dashboardSQLiteColumn); len(got) != 2 || got[1].Name != "title" {
		t.Fatalf("schema = %+v", got)
	}

	params, _ = json.Marshal(map[string]any{"app_id": "app-test", "database": "tasks", "table": "tasks", "limit": 1, "offset": 1})
	result, err := server.dispatchRPC(context.Background(), "sqlite/rows", params)
	if err != nil {
		t.Fatalf("sqlite/rows: %v", err)
	}
	rows := result.(dashboardSQLiteRows)
	if len(rows.Columns) != 2 || rows.Columns[1] != "title" || len(rows.Rows) != 1 || rows.Rows[0][1] != "two" {
		t.Fatalf("rows = %+v", rows)
	}

	params, _ = json.Marshal(map[string]any{"app_id": "app-test", "database": "/tmp/not-listed.sqlite"})
	if _, err := server.dispatchRPC(context.Background(), "sqlite/tables", params); err == nil {
		t.Fatal("sqlite/tables accepted a database outside status metadata")
	}
}
