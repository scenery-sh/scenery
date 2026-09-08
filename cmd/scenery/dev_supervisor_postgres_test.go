package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/postgresdb"
)

func TestMetadataWithRuntimePostgresDatabasesIncludesSchemas(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "app")
	supervisor := &devSupervisor{root: root + "-alias", worktreeRootPaths: &localagent.WorktreePaths{AppRoot: root}}
	supervisor.rememberWorktreeDatabase(postgresdb.Database{
		Database: "demo", Source: postgresdb.SourceExternal,
		Schemas: []postgresdb.Service{{Name: "main", Schema: "main"}, {Name: "reports", Schema: "reports"}},
	}, "demo")
	got := supervisor.metadataWithRuntimePostgresDatabases(json.RawMessage(`{"module_path":"example.test/app"}`), root)
	var payload struct {
		Databases []dashboardPostgresDatabase `json:"sql_databases"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Databases) != 1 || len(payload.Databases[0].Schemas) != 2 || payload.Databases[0].Source != "external" {
		t.Fatalf("databases = %+v", payload.Databases)
	}
	if got := supervisor.metadataWithRuntimePostgresDatabases(json.RawMessage(`{}`), root+"-other"); string(got) != `{}` {
		t.Fatalf("unrelated app received runtime databases: %s", got)
	}
	writer := &postgresMetadataWriter{}
	supervisor.storeWriter = writer
	supervisor.status = devdash.AppRecord{Root: supervisor.root, Metadata: json.RawMessage(`{}`)}
	if err := supervisor.persistStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	payload.Databases = nil
	if err := json.Unmarshal(writer.app.Metadata, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Databases) != 1 || len(payload.Databases[0].Schemas) != 2 {
		t.Fatalf("published databases = %+v", payload.Databases)
	}
}

type postgresMetadataWriter struct{ app devdash.AppRecord }

func (w *postgresMetadataWriter) UpsertApp(_ context.Context, app devdash.AppRecord) error {
	w.app = app
	return nil
}

func (*postgresMetadataWriter) WriteProcessEvent(context.Context, string, string, string, any) error {
	return nil
}
