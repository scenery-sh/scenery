package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
)

func TestDBListWithoutSQLDoesNotAllocateOrConnect(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"no-sql"}`)
	previous := openPostgresDatabase
	openPostgresDatabase = func(context.Context, string) (*sql.DB, error) {
		t.Fatal("no-SQL listing attempted a database connection")
		return nil, nil
	}
	t.Cleanup(func() { openPostgresDatabase = previous })
	var output bytes.Buffer
	if err := runDBList(t.Context(), &output, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var response databaseListResponse
	if err := decodeCLIJSON(output.Bytes(), &response); err != nil || response.Database != nil {
		t.Fatalf("no-SQL listing = %s, error = %v", output.String(), err)
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Directory); !os.IsNotExist(err) {
		t.Fatalf("read-only listing allocated worktree state: %v", err)
	}
}

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
	}
	dsn := "postgres://user:secret@localhost/demo"
	requirements := testSQLRequirements(t, "main")
	env, _, err := managedDatabaseEnv(context.Background(), root, cfg, requirements, []string{appDatabaseURLEnv + "=" + dsn})
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveDatabaseURLForConfigFromEnv(requirements, env)
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
	}
	dsn := "postgres://user:secret@localhost/demo"
	requirements := testSQLRequirements(t, "db", "search")
	env, _, err := managedDatabaseEnv(context.Background(), root, cfg, requirements, []string{"DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveDatabaseURLForConfigFromEnv(requirements, env)
	if err != nil {
		t.Fatalf("resolveDatabaseURLForConfig returned error: %v", err)
	}
	if !strings.Contains(got, "search_path=db%2Cscenery") {
		t.Fatalf("database URL = %q, want db search_path derived from %q", got, dsn)
	}
}
