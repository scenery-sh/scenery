package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePSQLArgs(t *testing.T) {
	opts, err := parsePSQLArgs([]string{"--app-root", "/tmp/app", "-c", "select 1"})
	if err != nil {
		t.Fatalf("parsePSQLArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("app root = %q", opts.AppRoot)
	}
	if got := strings.Join(opts.Args, " "); got != "-c select 1" {
		t.Fatalf("args = %q", got)
	}
}

func TestParsePSQLArgsRequiresAppRootValue(t *testing.T) {
	_, err := parsePSQLArgs([]string{"--app-root"})
	if err == nil || err.Error() != "missing value for --app-root" {
		t.Fatalf("parsePSQLArgs() error = %v", err)
	}
}

func TestDBCommandRejectsMissingOrUnknownSubcommand(t *testing.T) {
	if err := dbCommand(nil); err == nil || err.Error() != "usage: onlava db psql|sync|reset|drop|snapshot [--app-root <path>]" {
		t.Fatalf("dbCommand(nil) error = %v", err)
	}
	if err := dbCommand([]string{"vacuum"}); err == nil || err.Error() != `unknown db command "vacuum"` {
		t.Fatalf("dbCommand(vacuum) error = %v", err)
	}
}

func TestParseDBResetArgs(t *testing.T) {
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
	opts, err := parseDBSnapshotArgs([]string{"create", "before-refactor", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseDBSnapshotArgs returned error: %v", err)
	}
	if opts.Action != "create" || opts.Name != "before-refactor" || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDBSnapshotArgs([]string{"create"}); err == nil || err.Error() != "missing snapshot name" {
		t.Fatalf("missing name error = %v", err)
	}
	if _, err := parseDBSnapshotArgs([]string{}); err == nil || err.Error() != "missing db snapshot action create|restore" {
		t.Fatalf("missing action error = %v", err)
	}
}

func TestManagedPostgresSnapshotPathSanitizesName(t *testing.T) {
	root := t.TempDir()
	path, err := managedPostgresSnapshotPath(root, "session-a", "../Before Refactor!")
	if err != nil {
		t.Fatalf("managedPostgresSnapshotPath returned error: %v", err)
	}
	want := filepath.Join(root, ".onlava", "sessions", "session-a", "db", "snapshots", "before-refactor.sql")
	if path != want {
		t.Fatalf("snapshot path = %q, want %q", path, want)
	}
}

func TestBuildPSQLInvocationUsesDatabaseURLFromDotEnv(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DatabaseURL=postgres://localhost/from-file\nOTHER=two\n")
	invocation, err := buildPSQLInvocation(root, []string{"PATH=" + os.Getenv("PATH")}, psqlOptions{Args: []string{"-c", "select 1"}})
	if err != nil {
		t.Fatalf("buildPSQLInvocation returned error: %v", err)
	}
	if filepath.Base(invocation.Program) != "psql" {
		t.Fatalf("program = %q", invocation.Program)
	}
	if invocation.Dir != root {
		t.Fatalf("dir = %q, want %q", invocation.Dir, root)
	}
	if len(invocation.Args) < 3 {
		t.Fatalf("args = %+v", invocation.Args)
	}
	if invocation.Args[0] != "postgres://localhost/from-file" {
		t.Fatalf("dsn arg = %q", invocation.Args[0])
	}
	if got := strings.Join(invocation.Args[1:], " "); got != "-c select 1" {
		t.Fatalf("forwarded args = %q", got)
	}
	if !containsEnv(invocation.Env, "OTHER=two") {
		t.Fatalf("env missing .env value: %+v", invocation.Env)
	}
}

func TestBuildPSQLInvocationPrefersProcessEnv(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DATABASE_URL=postgres://localhost/from-file\n")
	t.Setenv("DATABASE_URL", "postgres://localhost/from-env")
	invocation, err := buildPSQLInvocation(root, os.Environ(), psqlOptions{})
	if err != nil {
		t.Fatalf("buildPSQLInvocation returned error: %v", err)
	}
	if invocation.Args[0] != "postgres://localhost/from-env" {
		t.Fatalf("dsn arg = %q", invocation.Args[0])
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
