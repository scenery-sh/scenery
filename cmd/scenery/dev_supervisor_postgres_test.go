package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"scenery.sh/internal/app"
)

func TestMetadataWithRuntimePostgresDatabasesIncludesSchemas(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	cfg := app.Config{
		Name: "demo",
		ID:   "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"main":    {},
			"reports": {},
		}},
	}
	got := metadataWithRuntimePostgresDatabases(json.RawMessage(`{"module_path":"example.test/app"}`), root, cfg, true)
	var payload struct {
		Databases []dashboardPostgresDatabase `json:"sql_databases"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Databases) != 1 || len(payload.Databases[0].Schemas) != 2 {
		t.Fatalf("databases = %+v", payload.Databases)
	}
}
