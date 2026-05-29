package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

func TestResolveManagedPostgresPlanDefaults(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
		}},
	}
	session := &localagent.Session{
		SessionID: "feature-db-123abc",
		BaseAppID: "demo-app",
	}
	plan, err := resolveManagedPostgresPlan(cfg, session, []string{
		devPostgresAdminURLEnv + "=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("resolveManagedPostgresPlan returned error: %v", err)
	}
	if plan.Version != devPostgresDefaultVersion || plan.Isolation != devPostgresDefaultIsolation {
		t.Fatalf("plan defaults = %+v", plan)
	}
	if plan.DatabaseName != "demo_app_feature_db_123abc" {
		t.Fatalf("database name = %q", plan.DatabaseName)
	}
	if plan.DatabaseURL != "postgres://postgres:postgres@127.0.0.1:5432/demo_app_feature_db_123abc?sslmode=disable" {
		t.Fatalf("database URL = %q", plan.DatabaseURL)
	}
}

func TestManagedPostgresPlanRejectsUnsupportedIsolation(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres", Isolation: "schema"},
		}},
	}
	_, err := resolveManagedPostgresPlan(cfg, &localagent.Session{SessionID: "session", BaseAppID: "demo"}, []string{
		devPostgresAdminURLEnv + "=postgres://localhost/postgres",
	})
	if err == nil || !strings.Contains(err.Error(), `isolation "schema" is not supported yet`) {
		t.Fatalf("error = %v", err)
	}
}

func TestManagedPostgresEnvOverridesExplicitDatabaseURL(t *testing.T) {
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
		}},
	}
	prevEnsure := ensureManagedPostgresDatabaseFn
	defer func() { ensureManagedPostgresDatabaseFn = prevEnsure }()
	ensureManagedPostgresDatabaseFn = func(_ context.Context, adminURL, dbName string) error {
		if adminURL != "postgres://localhost/postgres" || dbName != "demo_session" {
			t.Fatalf("ensure managed postgres got adminURL=%q dbName=%q", adminURL, dbName)
		}
		return nil
	}
	env, err := managedPostgresEnv(t.Context(), cfg, &localagent.Session{
		SessionID: "session",
		BaseAppID: "demo",
	}, []string{
		devPostgresAdminURLEnv + "=postgres://localhost/postgres",
		"DatabaseURL=postgres://localhost/user",
	}, nil)
	if err != nil {
		t.Fatalf("managedPostgresEnv returned error: %v", err)
	}
	for _, want := range []string{
		"DatabaseURL=postgres://localhost/demo_session",
		"DATABASE_URL=postgres://localhost/demo_session",
		"ONLAVA_MANAGED_DATABASE_NAME=demo_session",
	} {
		if !containsString(env, want) {
			t.Fatalf("managed env missing %q: %+v", want, env)
		}
	}
}

func TestManagedPostgresEnvAllowsExplicitExternalDatabaseOptOut(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
		}},
	}
	env, err := managedPostgresEnv(t.Context(), cfg, nil, []string{
		devPostgresExternalEnv + "=1",
		"DatabaseURL=postgres://localhost/user",
	}, nil)
	if err != nil {
		t.Fatalf("managedPostgresEnv returned error: %v", err)
	}
	if env != nil {
		t.Fatalf("managed env = %+v, want nil", env)
	}
}

func TestManagedPostgresAdminURLCanComeFromAgentSubstrate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstratePostgres,
		OwnerPID: os.Getpid(),
		URLs:     map[string]string{"admin": "postgres://localhost/postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	env := envWithManagedPostgresAgentAdminURL(ctx, []string{"A=1"}, client)
	if !containsString(env, devPostgresAdminURLEnv+"=postgres://localhost/postgres") {
		t.Fatalf("env = %+v", env)
	}
}

func TestManagedPostgresDatabaseNameFitsPostgresIdentifierLimit(t *testing.T) {
	t.Parallel()

	got := managedPostgresDatabaseName(strings.Repeat("very-long-app-", 8), strings.Repeat("feature-branch-", 8))
	if len(got) > 63 {
		t.Fatalf("database name length = %d, value %q", len(got), got)
	}
	if strings.ContainsAny(got, "-.") {
		t.Fatalf("database name contains unsafe punctuation: %q", got)
	}
}

func TestPostgresDatabaseURLRequiresPostgresScheme(t *testing.T) {
	t.Parallel()

	if _, err := postgresDatabaseURL("mysql://localhost/db", "session"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestPostgresDatabaseURLAcceptsUnixSocketHostQuery(t *testing.T) {
	t.Parallel()

	got, err := postgresDatabaseURL("postgres://onlava@/postgres?host=%2Ftmp%2Fonlava+pg&port=55432&sslmode=disable", "session")
	if err != nil {
		t.Fatalf("postgresDatabaseURL returned error: %v", err)
	}
	if got != "postgres://onlava@/session?host=%2Ftmp%2Fonlava+pg&port=55432&sslmode=disable" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestLocalPostgresAdminURLUsesUnixSocketQuery(t *testing.T) {
	t.Parallel()

	got := localPostgresAdminURL("/tmp/onlava pg", 55432)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse local admin URL: %v", err)
	}
	if parsed.Scheme != "postgres" || parsed.User.Username() != "onlava" || parsed.Host != "" || parsed.Path != "/postgres" {
		t.Fatalf("admin URL = %q", got)
	}
	if parsed.Query().Get("host") != "/tmp/onlava pg" || parsed.Query().Get("port") != "55432" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("admin URL query = %q", parsed.RawQuery)
	}
}

func TestLocalPostgresTCPAdminURL(t *testing.T) {
	t.Parallel()

	got := localPostgresTCPAdminURL(55432)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse local tcp admin URL: %v", err)
	}
	if parsed.User.Username() != "onlava" {
		t.Fatalf("admin URL user = %q", parsed.User.Username())
	}
	if password, _ := parsed.User.Password(); password != "postgres" {
		t.Fatalf("admin URL password = %q", password)
	}
	if parsed.Host != "127.0.0.1:55432" || parsed.Path != "/postgres" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("admin URL = %q", got)
	}
}

func TestPostgresMajorVersionFromOutput(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"postgres (PostgreSQL) 14.0": "14",
		"postgres (PostgreSQL) 18":   "18",
		"postgres (PostgreSQL) 9.6":  "9",
	} {
		got, err := postgresMajorVersionFromOutput(input)
		if err != nil {
			t.Fatalf("postgresMajorVersionFromOutput(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("postgresMajorVersionFromOutput(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestManagedPostgresServerArgsEnableLogicalReplication(t *testing.T) {
	t.Parallel()

	got := strings.Join(managedPostgresServerArgs(), " ")
	for _, want := range []string{
		"wal_level=logical",
		"max_wal_senders=10",
		"max_replication_slots=10",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("managed Postgres server args %q missing %q", got, want)
		}
	}
}

func TestResolveLocalPostgresBinariesFindsSibling(t *testing.T) {
	dir := t.TempDir()
	initdb := filepath.Join(dir, "initdb")
	postgres := filepath.Join(dir, "postgres")
	if err := os.WriteFile(initdb, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(postgres, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prevLookPath := execLookPath
	defer func() { execLookPath = prevLookPath }()
	execLookPath = func(file string) (string, error) {
		if file == "initdb" {
			return initdb, nil
		}
		return "", os.ErrNotExist
	}
	binaries, err := resolveLocalPostgresBinaries(nil)
	if err != nil {
		t.Fatalf("resolveLocalPostgresBinaries returned error: %v", err)
	}
	if binaries.InitDB != initdb || binaries.Postgres != postgres {
		t.Fatalf("binaries = %+v", binaries)
	}
}

func TestLocalPostgresPortPersists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := localPostgresPort(root)
	if err != nil {
		t.Fatalf("localPostgresPort returned error: %v", err)
	}
	second, err := localPostgresPort(root)
	if err != nil {
		t.Fatalf("localPostgresPort second call returned error: %v", err)
	}
	if first != second {
		t.Fatalf("port not persisted: first %d second %d", first, second)
	}
}

func TestManagedElectricBackendsAndEnv(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"electric": {
				Kind:  "electric",
				Route: "electric",
				Env: map[string]string{
					"DATABASE_URL":             "$DatabaseURL",
					"ELECTRIC_USAGE_REPORTING": "false",
				},
			},
		}},
	}
	baseEnv := []string{
		devElectricUpstreamEnv + "=http://127.0.0.1:3001",
		"DatabaseURL=postgres://localhost/session",
	}
	backends, err := managedElectricBackends(cfg, baseEnv)
	if err != nil {
		t.Fatalf("managedElectricBackends returned error: %v", err)
	}
	if backends["electric"].Addr != "127.0.0.1:3001" {
		t.Fatalf("electric backend = %+v", backends["electric"])
	}

	env, err := managedElectricEnv(cfg, &localagent.Session{Routes: map[string]string{
		"electric": "http://electric.session.onlava.localhost",
	}}, baseEnv)
	if err != nil {
		t.Fatalf("managedElectricEnv returned error: %v", err)
	}
	if !containsString(env, "ELECTRIC_URL=http://electric.session.onlava.localhost") {
		t.Fatalf("electric env missing route URL: %+v", env)
	}
	if containsString(env, "DATABASE_URL=postgres://localhost/session") || containsString(env, "ELECTRIC_USAGE_REPORTING=false") {
		t.Fatalf("electric service container env leaked into app env: %+v", env)
	}
}

func TestManagedElectricProcessEnvExpandsDatabaseURL(t *testing.T) {
	t.Parallel()

	plan := &managedElectricPlan{
		Env: map[string]string{
			"DATABASE_URL":             "$DatabaseURL",
			"ELECTRIC_USAGE_REPORTING": "false",
		},
	}
	env := managedElectricProcessEnv(plan, []string{"PATH=/bin", "DATABASE_URL=postgres://old/db"}, "postgres://session/db", 3456, "onlava_session")
	for _, want := range []string{
		"DATABASE_URL=postgres://session/db",
		"DatabaseURL=postgres://session/db",
		"ELECTRIC_PORT=3456",
		"PORT=3456",
		"ELECTRIC_REPLICATION_STREAM_ID=onlava_session",
		"ELECTRIC_USAGE_REPORTING=false",
	} {
		if !containsString(env, want) {
			t.Fatalf("managed electric env missing %q: %+v", want, env)
		}
	}
}

func TestManagedElectricProcessEnvAllowsConfiguredReplicationStreamID(t *testing.T) {
	t.Parallel()

	plan := &managedElectricPlan{Env: map[string]string{
		"ELECTRIC_REPLICATION_STREAM_ID": "custom_stream",
	}}
	env := managedElectricProcessEnv(plan, []string{"PATH=/bin"}, "postgres://session/db", 3456, "onlava_session")
	if !containsString(env, "ELECTRIC_REPLICATION_STREAM_ID=custom_stream") {
		t.Fatalf("managed electric env did not preserve configured stream id: %+v", env)
	}
}

func TestManagedElectricReplicationStreamIDUsesSessionIdentifier(t *testing.T) {
	t.Parallel()

	got := managedElectricReplicationStreamID(&localagent.Session{SessionID: "feature/my-worktree-123456"})
	if got != "onlava_feature_my_worktree_123456" {
		t.Fatalf("stream id = %q", got)
	}
}

func TestManagedElectricDatabaseURLPrefersManagedPostgres(t *testing.T) {
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
			"electric": {Kind: "electric"},
		}},
	}
	prevEnsure := ensureManagedPostgresDatabaseFn
	defer func() { ensureManagedPostgresDatabaseFn = prevEnsure }()
	ensureManagedPostgresDatabaseFn = func(_ context.Context, _ string, dbName string) error {
		if dbName != "demo_session" {
			t.Fatalf("dbName = %q", dbName)
		}
		return nil
	}
	got, err := managedElectricDatabaseURL(t.Context(), t.TempDir(), cfg, &localagent.Session{
		SessionID: "session",
		BaseAppID: "demo",
	}, &managedElectricPlan{ServiceName: "electric"}, []string{
		devPostgresAdminURLEnv + "=postgres://localhost/postgres",
		"DatabaseURL=postgres://localhost/explicit",
	}, nil)
	if err != nil {
		t.Fatalf("managedElectricDatabaseURL returned error: %v", err)
	}
	if got != "postgres://localhost/demo_session" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestManagedElectricDatabaseURLAllowsExternalPostgresOptOut(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
			"electric": {Kind: "electric"},
		}},
	}
	got, err := managedElectricDatabaseURL(t.Context(), t.TempDir(), cfg, &localagent.Session{
		SessionID: "session",
		BaseAppID: "demo",
	}, &managedElectricPlan{ServiceName: "electric"}, []string{
		devPostgresExternalEnv + "=1",
		devPostgresAdminURLEnv + "=postgres://localhost/postgres",
		"DatabaseURL=postgres://localhost/explicit",
	}, nil)
	if err != nil {
		t.Fatalf("managedElectricDatabaseURL returned error: %v", err)
	}
	if got != "postgres://localhost/explicit" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestManagedElectricContainerEnvUsesInternalPort(t *testing.T) {
	t.Parallel()

	plan := &managedElectricPlan{
		Env: map[string]string{"ELECTRIC_INSECURE": "true"},
	}
	env := managedElectricContainerEnv(plan, []string{"PATH=/bin"}, "postgres://onlava:postgres@127.0.0.1:55432/session?sslmode=disable", "onlava_session")
	for _, want := range []string{
		"DATABASE_URL=postgres://onlava:postgres@host.docker.internal:55432/session?sslmode=disable",
		"ELECTRIC_PORT=3000",
		"PORT=3000",
		"ELECTRIC_REPLICATION_STREAM_ID=onlava_session",
		"ELECTRIC_INSECURE=true",
	} {
		if !containsString(env, want) {
			t.Fatalf("container env missing %q: %+v", want, env)
		}
	}
}

func TestStartManagedElectricServiceRequiresLaunchSource(t *testing.T) {
	prevLookPath := execLookPath
	defer func() { execLookPath = prevLookPath }()
	execLookPath = func(file string) (string, error) { return "", os.ErrNotExist }
	_, _, err := startManagedElectricService(t.Context(), t.TempDir(), app.Config{Name: "demo"}, &localagent.Session{
		SessionID: "session",
		StateRoot: t.TempDir(),
	}, &managedElectricPlan{
		ServiceName: "electric",
		Route:       "electric",
	}, []string{"DATABASE_URL=postgres://localhost/session"}, nil)
	if err == nil || !strings.Contains(err.Error(), devElectricBinEnv) {
		t.Fatalf("error = %v", err)
	}
}
