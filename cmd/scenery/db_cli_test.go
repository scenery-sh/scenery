package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/sqlitedb"
)

func TestDBCommandRejectsMissingOrRemovedSubcommand(t *testing.T) {
	t.Parallel()

	if err := dbCommand(nil); err == nil || err.Error() != "usage: scenery db list|path|shell|apply|seed|setup|reset|drop|snapshot|diff|branch|server [--app-root <path>]" {
		t.Fatalf("dbCommand(nil) error = %v", err)
	}
	for _, cmd := range []string{"vacuum", "psql", "postgres"} {
		if err := dbCommand([]string{cmd}); err == nil || err.Error() != `unknown db command "`+cmd+`"` {
			t.Fatalf("dbCommand(%s) error = %v", cmd, err)
		}
	}
}

func TestParseSQLiteDBArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseSQLiteDBArgs([]string{"--app-root", "/tmp/app", "--json", "main", ".schema"}, false)
	if err != nil {
		t.Fatalf("parseSQLiteDBArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Service != "main" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if got := strings.Join(opts.Args, " "); got != ".schema" {
		t.Fatalf("args = %q", got)
	}
	if _, err := parseSQLiteDBArgs([]string{"--app-root"}, false); err == nil || err.Error() != "missing value for --app-root" {
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

func TestDBTargetResolutionSkipsOtherEngineWhenServiceKnown(t *testing.T) {
	t.Parallel()

	cfg := app.Config{Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
		"cache":   {Kind: "sqlite"},
		"reports": {Kind: "postgres"},
	}}}
	if shouldResolvePostgresForDBTarget(cfg, "cache") {
		t.Fatalf("sqlite target should not resolve postgres services")
	}
	if shouldResolveSQLiteForDBTarget(cfg, "reports") {
		t.Fatalf("postgres target should not resolve sqlite services")
	}
	if !shouldResolveSQLiteForDBTarget(cfg, "discovered") {
		t.Fatalf("unknown target may be a discovered sqlite service")
	}
}

func TestDBBranchAllowsSQLiteBranchesInMixedDatabaseApp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"demo","dev":{"services":{"cache":{"kind":"sqlite"},"reports":{"kind":"postgres"}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var stdout bytes.Buffer
	err := runDBBranchCommand(context.Background(), &stdout, []string{"status", "--app-root", root})
	if err != nil {
		t.Fatalf("mixed app branch status returned error: %v", err)
	}
}

func TestDBBranchRejectsPostgresOnlyApp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"demo","dev":{"services":{"reports":{"kind":"postgres"}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var stdout bytes.Buffer
	err := runDBBranchCommand(context.Background(), &stdout, []string{"status", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), "postgres services are not branchable") {
		t.Fatalf("postgres-only branch status error = %v", err)
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

func TestParseDBSnapshotArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDBSnapshotArgs([]string{"create", "--name", "before-refactor", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseDBSnapshotArgs returned error: %v", err)
	}
	if opts.Action != "create" || opts.Name != "before-refactor" || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDBSnapshotArgs([]string{"create"}); err == nil || !strings.Contains(err.Error(), "db snapshot requires --name") {
		t.Fatalf("missing name error = %v", err)
	}
	if _, err := parseDBSnapshotArgs([]string{}); err == nil || !strings.Contains(err.Error(), "scenery db snapshot create|restore") {
		t.Fatalf("missing action error = %v", err)
	}
}

func TestDatabaseListRecordsIncludeBothEngines(t *testing.T) {
	t.Parallel()

	records := databaseListRecords(
		[]sqlitedb.Service{{Name: "cache", FileLabel: "cache", Path: "/tmp/cache.sqlite", URL: "sqlite:///tmp/cache.sqlite", DatabaseURLEnv: "CACHE_DATABASE_URL"}},
		[]postgresdb.Service{{Name: "reports", Database: "demo_reports_abcd1234", URL: "postgres://user:secret@localhost/reports", DatabaseURLEnv: "REPORTS_DATABASE_URL", Source: postgresdb.SourceManaged}},
	)
	if len(records) != 2 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Engine != "sqlite" || records[0].Service != "cache" || records[0].Path == "" {
		t.Fatalf("sqlite record = %+v", records[0])
	}
	if records[1].Engine != "postgres" || records[1].Service != "reports" || records[1].Database == "" || strings.Contains(records[1].URL, "secret") {
		t.Fatalf("postgres record = %+v", records[1])
	}
}

func TestResolveDatabaseURLForConfigUsesSQLiteService(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		ID:   "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"main": {Kind: "sqlite", Database: "demo.sqlite"},
		}},
	}
	got, err := resolveDatabaseURLForConfig(context.Background(), root, cfg, []string{
		appDatabaseURLEnv + "=sqlite:///stale.sqlite",
		legacyDatabaseURLEnv + "=sqlite:///poison.sqlite",
	}, true)
	if err != nil {
		t.Fatalf("resolveDatabaseURLForConfig returned error: %v", err)
	}
	wantPath := filepath.Join(root, ".scenery", "sqlite", "local", "demo.sqlite")
	if got != sqlitedb.URLForPath(wantPath) {
		t.Fatalf("database URL = %q, want %q", got, sqlitedb.URLForPath(wantPath))
	}
}

func TestResolveDatabaseURLForConfigDefaultsToDBService(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"db":     {Kind: "sqlite", Database: "main", DatabaseURLEnv: appDatabaseURLEnv},
			"search": {Kind: "sqlite"},
		}},
	}
	got, err := resolveDatabaseURLForConfig(context.Background(), root, cfg, nil, true)
	if err != nil {
		t.Fatalf("resolveDatabaseURLForConfig returned error: %v", err)
	}
	want := sqlitedb.URLForPath(filepath.Join(root, ".scenery", "sqlite", "local", "main.sqlite"))
	if got != want {
		t.Fatalf("database URL = %q, want %q", got, want)
	}
}
