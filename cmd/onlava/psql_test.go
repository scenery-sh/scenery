package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/neonselfhost"
)

func TestParsePSQLArgs(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	_, err := parsePSQLArgs([]string{"--app-root"})
	if err == nil || err.Error() != "missing value for --app-root" {
		t.Fatalf("parsePSQLArgs() error = %v", err)
	}
}

func TestDBCommandRejectsMissingOrUnknownSubcommand(t *testing.T) {
	t.Parallel()

	if err := dbCommand(nil); err == nil || err.Error() != "usage: onlava db psql|apply|seed|setup|reset|drop|snapshot|branch|neon [--app-root <path>]" {
		t.Fatalf("dbCommand(nil) error = %v", err)
	}
	if err := dbCommand([]string{"vacuum"}); err == nil || err.Error() != `unknown db command "vacuum"` {
		t.Fatalf("dbCommand(vacuum) error = %v", err)
	}
	if err := dbCommand([]string{"sync"}); err == nil || err.Error() != `unknown db command "sync"` {
		t.Fatalf("dbCommand(sync) error = %v", err)
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

	opts, err := parseDBSnapshotArgs([]string{"create", "before-refactor", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseDBSnapshotArgs returned error: %v", err)
	}
	if opts.Action != "create" || opts.Name != "before-refactor" || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDBSnapshotArgs([]string{"create"}); err == nil || !strings.Contains(err.Error(), "onlava db snapshot create <name>") {
		t.Fatalf("missing name error = %v", err)
	}
	if _, err := parseDBSnapshotArgs([]string{}); err == nil || !strings.Contains(err.Error(), "onlava db snapshot create <name>") || !strings.Contains(err.Error(), "onlava db snapshot restore <name>") {
		t.Fatalf("missing action error = %v", err)
	}
}

func TestManagedPostgresSnapshotPathSanitizesName(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func TestBuildPSQLInvocationMovesPasswordToPGPassword(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DatabaseURL=postgres://cloud_admin:cloud_admin@127.0.0.1:55434/demo?sslmode=disable\n")
	invocation, err := buildPSQLInvocation(root, []string{"PATH=" + os.Getenv("PATH"), "PGPASSWORD=stale"}, psqlOptions{})
	if err != nil {
		t.Fatalf("buildPSQLInvocation returned error: %v", err)
	}
	if invocation.Args[0] != "postgres://cloud_admin@127.0.0.1:55434/demo?sslmode=disable" {
		t.Fatalf("dsn arg = %q", invocation.Args[0])
	}
	if got := envValueFromList(invocation.Env, "PGPASSWORD"); got != "cloud_admin" {
		t.Fatalf("PGPASSWORD = %q", got)
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

func TestResolveDatabaseURLForConfigExternalModeRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
		}},
	}
	got, err := resolveDatabaseURLForConfig(context.Background(), t.TempDir(), cfg, []string{
		devPostgresExternalEnv + "=1",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
		appDatabaseURLEnv + "=postgres://localhost/explicit",
	}, true)
	if err != nil {
		t.Fatalf("resolveDatabaseURLForConfig returned error: %v", err)
	}
	if got != "postgres://localhost/explicit" {
		t.Fatalf("database URL = %q", got)
	}

	_, err = resolveDatabaseURLForConfig(context.Background(), t.TempDir(), cfg, []string{
		devPostgresExternalEnv + "=1",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	}, true)
	if err == nil || !strings.Contains(err.Error(), "requires DatabaseURL") || !strings.Contains(err.Error(), "DATABASE_URL is ignored") {
		t.Fatalf("resolveDatabaseURLForConfig external error = %v", err)
	}
}

func TestResolveDatabaseURLForConfigUsesReadyNeonBranchConnection(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", Project: "demo"},
		}},
	}
	_, err := resolveDatabaseURLForConfig(context.Background(), root, cfg, nil, true)
	if err == nil || !strings.Contains(err.Error(), "has no worktree branch pin") {
		t.Fatalf("missing pin error = %v", err)
	}
	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-selfhost",
		"project": "demo",
		"parent_branch": "main",
		"branch": "demo/review-a",
		"branch_id": "br-local-review",
		"database": "demo",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)
	_, err = resolveDatabaseURLForConfig(context.Background(), root, cfg, nil, true)
	if err == nil || !strings.Contains(err.Error(), `pin "demo/review-a"`) || !strings.Contains(err.Error(), "no Onlava-owned lease") {
		t.Fatalf("pending connection error = %v", err)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read pin ok=%v err=%v", ok, err)
	}
	if err := upsertNeonBranchLease(pin); err != nil {
		t.Fatalf("upsert lease: %v", err)
	}
	markNeonLeaseReadyForTest(t, pin, neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55434,
		Database: "demo",
		Role:     "cloud_admin",
		SSLMode:  "disable",
	})
	got, err := resolveDatabaseURLForConfig(context.Background(), root, cfg, nil, true)
	if err != nil {
		t.Fatalf("resolve ready Neon URL: %v", err)
	}
	if got != "postgres://cloud_admin:cloud_admin@127.0.0.1:55434/demo?sslmode=disable" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestBuildPSQLInvocationUsesReadyNeonPasswordWithoutPrompt(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", Project: "demo"},
		}},
	}
	pin, err := buildWorktreeDBPin(root, cfg, "demo/review-a")
	if err != nil {
		t.Fatalf("build pin: %v", err)
	}
	if err := writeWorktreeDBPin(root, pin); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	markNeonLeaseReadyForTest(t, pin, neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55434,
		Database: "demo",
		Role:     "cloud_admin",
		SSLMode:  "disable",
		Source:   neonSelfhostBranchDriverEndpointSource,
	})
	invocation, err := buildPSQLInvocationForConfig(context.Background(), root, cfg, []string{"PATH=" + os.Getenv("PATH")}, psqlOptions{UseManaged: true})
	if err != nil {
		t.Fatalf("buildPSQLInvocationForConfig returned error: %v", err)
	}
	if invocation.Args[0] != "postgres://cloud_admin@127.0.0.1:55434/demo?sslmode=disable" {
		t.Fatalf("dsn arg = %q", invocation.Args[0])
	}
	if got := envValueFromList(invocation.Env, "PGPASSWORD"); got != "cloud_admin" {
		t.Fatalf("PGPASSWORD = %q", got)
	}
}

func TestCreateNeonSelfhostSnapshotUsesComputePgDump(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root, pin, connection := readyNeonSnapshotTargetForTest(t)
	path := filepath.Join(t.TempDir(), "snapshot.sql")
	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "exec" ] && [ "$5" = "pg_dump" ]; then
  printf 'create table public.snapshot_probe(id text);\n'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)

	ok, err := createNeonSelfhostSnapshot(context.Background(), dbSnapshotTarget{
		Kind:        "neon",
		DatabaseURL: connection.DatabaseURL,
		NeonPin:     &pin,
		NeonConn:    &connection,
	}, path)
	if err != nil || !ok {
		t.Fatalf("createNeonSelfhostSnapshot ok=%v err=%v", ok, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(data), "snapshot_probe") {
		t.Fatalf("snapshot data = %q", string(data))
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "exec -e PGPASSWORD=cloud_admin onlava-neon-compute-review pg_dump") ||
		!strings.Contains(log, "--no-publications --no-subscriptions") ||
		!strings.Contains(log, "-p 55433") {
		t.Fatalf("docker log = %q", log)
	}
	if root == "" {
		t.Fatal("unused root")
	}
}

func TestRestoreNeonSelfhostSnapshotUsesComputePSQL(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	_, pin, connection := readyNeonSnapshotTargetForTest(t)
	path := filepath.Join(t.TempDir(), "snapshot.sql")
	if err := os.WriteFile(path, []byte("select 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	stdinPath := filepath.Join(bin, "stdin.sql")
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = "exec" ] && [ "$2" = "-i" ] && [ "$6" = "psql" ]; then
  cat > "$DOCKER_STDIN"
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)
	t.Setenv("DOCKER_STDIN", stdinPath)

	ok, err := restoreNeonSelfhostSnapshot(context.Background(), dbSnapshotTarget{
		Kind:        "neon",
		DatabaseURL: connection.DatabaseURL,
		NeonPin:     &pin,
		NeonConn:    &connection,
	}, path)
	if err != nil || !ok {
		t.Fatalf("restoreNeonSelfhostSnapshot ok=%v err=%v", ok, err)
	}
	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read docker stdin: %v", err)
	}
	if string(stdin) != "select 1;\n" {
		t.Fatalf("stdin = %q", string(stdin))
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "exec -i -e PGPASSWORD=cloud_admin onlava-neon-compute-review psql") || !strings.Contains(log, "-p 55433") {
		t.Fatalf("docker log = %q", log)
	}
}

func readyNeonSnapshotTargetForTest(t *testing.T) (string, worktreeDBPin, neonBranchConnectionInfo) {
	t.Helper()
	pin := worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfhostProvider,
		Project:       "demo",
		ParentBranch:  "main",
		Branch:        "review",
		BranchID:      "br-local-review",
		Database:      "demo",
		Role:          "cloud_admin",
		CreatedBy:     "onlava",
	}
	root := filepath.Join(os.Getenv("ONLAVA_AGENT_HOME"), "agent", "substrates", "neon")
	state := neonselfhost.NewBackendState()
	project := neonselfhost.NewBackendProject(pin.Project, 16)
	project.TenantID = "tenant-test"
	project.Branches[pin.BranchID] = neonselfhost.BackendBranch{
		Project:          pin.Project,
		Branch:           pin.Branch,
		TimelineID:       "11111111111111111111111111111111",
		ComputeContainer: "onlava-neon-compute-review",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         pin.Database,
		Role:             pin.Role,
		Status:           "ready",
	}
	state.Projects[pin.Project] = project
	if err := neonselfhost.WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend state: %v", err)
	}
	endpoint := neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55441,
		Database: "demo",
		Role:     "cloud_admin",
		SSLMode:  "disable",
		Source:   neonSelfhostBranchDriverEndpointSource,
	}
	dsn, err := neonEndpointDatabaseURL(pin, endpoint)
	if err != nil {
		t.Fatalf("database URL: %v", err)
	}
	return root, pin, neonBranchConnectionInfo{
		DatabaseURL:  dsn,
		DatabaseName: "demo",
		Endpoint:     endpoint,
	}
}

func TestResolveDatabaseURLForConfigRefusesProtectedParentNeonBranch(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", Project: "demo", ParentBranch: "main"},
		}},
	}
	pin, err := buildWorktreeDBPin(root, cfg, "main")
	if err != nil {
		t.Fatalf("build pin: %v", err)
	}
	if err := writeWorktreeDBPin(root, pin); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	markNeonLeaseReadyForTest(t, pin, neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55434,
		Database: "demo",
		Role:     "cloud_admin",
		SSLMode:  "disable",
	})

	_, err = resolveDatabaseURLForConfig(context.Background(), root, cfg, nil, true)
	if err == nil || !strings.Contains(err.Error(), "protected parent branch") {
		t.Fatalf("protected parent URL error = %v", err)
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
