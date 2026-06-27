package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/sqlitedb"
)

func TestDBCommandRejectsMissingOrRemovedSubcommand(t *testing.T) {
	t.Parallel()

	if err := dbCommand(nil); err == nil || err.Error() != "usage: scenery db list|path|shell|apply|seed|setup|reset|drop|snapshot|diff|branch [--app-root <path>]" {
		t.Fatalf("dbCommand(nil) error = %v", err)
	}
	for _, cmd := range []string{"vacuum", "p" + "sql", "post" + "gres"} {
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

func TestResolveDatabaseURLForConfigRequiresSingleSQLiteService(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"main":   {Kind: "sqlite"},
			"search": {Kind: "sqlite"},
		}},
	}
	_, err := resolveDatabaseURLForConfig(context.Background(), t.TempDir(), cfg, nil, true)
	if err == nil || !strings.Contains(err.Error(), "sqlite service name is required") {
		t.Fatalf("resolveDatabaseURLForConfig error = %v", err)
	}
}
