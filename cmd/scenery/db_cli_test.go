package main

import (
	"context"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
)

func TestDBCommandRejectsMissingOrRemovedSubcommand(t *testing.T) {
	t.Parallel()

	if err := dbCommand(nil); err == nil || err.Error() != "usage: scenery db list|shell|apply|seed|setup|reset|drop|server [--app-root <path>]" {
		t.Fatalf("dbCommand(nil) error = %v", err)
	}
	for _, cmd := range []string{"vacuum", "psql", "postgres", "path", "branch"} {
		if err := dbCommand([]string{cmd}); err == nil || err.Error() != `unknown db command "`+cmd+`"` {
			t.Fatalf("dbCommand(%s) error = %v", cmd, err)
		}
	}
}

func TestParseDBCLIArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDBCLIArgs([]string{"--app-root", "/tmp/app", "-o", "json", "main", ".schema"}, false)
	if err != nil {
		t.Fatalf("parseDBCLIArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Service != "main" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if got := strings.Join(opts.Args, " "); got != ".schema" {
		t.Fatalf("args = %q", got)
	}
	if _, err := parseDBCLIArgs([]string{"--app-root"}, false); err == nil || err.Error() != "missing value for --app-root" {
		t.Fatalf("missing app root error = %v", err)
	}
}

func TestParseDBTargetArgsAllowsYesAfterService(t *testing.T) {
	t.Parallel()

	opts, err := parseDBTargetArgs([]string{"reports", "--yes", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseDBTargetArgs returned error: %v", err)
	}
	if opts.Service != "reports" || !opts.Yes || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDBTargetArgs([]string{"reports", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("extra arg error = %v", err)
	}
}

func TestParseDBResetArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDBResetArgs([]string{"--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseDBResetArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("app root = %q", opts.AppRoot)
	}
	if _, err := parseDBResetArgs([]string{"--app-root"}); err == nil || err.Error() != "missing value for --app-root" {
		t.Fatalf("parseDBResetArgs missing value error = %v", err)
	}
}

func TestDatabaseListRecordsIncludesPostgresSchemas(t *testing.T) {
	t.Parallel()

	record := databaseListRecordFromDatabase(context.Background(), postgresdb.Database{
		Database: "demo_abcd1234",
		URL:      "postgres://user:secret@localhost/app",
		Source:   postgresdb.SourceManaged,
		Schemas: []postgresdb.Service{
			{Name: "reports", Schema: "reports", URL: "postgres://user:secret@localhost/app?search_path=reports%2Cscenery"},
		},
	})
	if record.Name != "demo_abcd1234" || record.Source != "managed" || len(record.Schemas) != 1 || strings.Contains(record.URL, "secret") {
		t.Fatalf("postgres record = %+v", record)
	}
	if record.Schemas[0].Service != "reports" || record.Schemas[0].Schema != "reports" || strings.Contains(record.Schemas[0].URL, "secret") {
		t.Fatalf("postgres schema record = %+v", record.Schemas[0])
	}
}

func TestResolveDatabaseURLForConfigUsesAppDatabaseURL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		ID:   "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"main": {},
		}},
	}
	dsn := "postgres://user:secret@localhost/demo"
	got, err := resolveDatabaseURLForConfig(context.Background(), root, cfg, []string{appDatabaseURLEnv + "=" + dsn}, true)
	if err != nil {
		t.Fatalf("resolveDatabaseURLForConfig returned error: %v", err)
	}
	if !strings.Contains(got, "search_path=main%2Cscenery") {
		t.Fatalf("database URL = %q, want main search_path derived from %q", got, dsn)
	}
}

func TestResolveDatabaseURLForConfigDefaultsToDBService(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"db":     {},
			"search": {},
		}},
	}
	dsn := "postgres://user:secret@localhost/demo"
	got, err := resolveDatabaseURLForConfig(context.Background(), root, cfg, []string{"DATABASE_URL=" + dsn}, true)
	if err != nil {
		t.Fatalf("resolveDatabaseURLForConfig returned error: %v", err)
	}
	if !strings.Contains(got, "search_path=db%2Cscenery") {
		t.Fatalf("database URL = %q, want db search_path derived from %q", got, dsn)
	}
}
