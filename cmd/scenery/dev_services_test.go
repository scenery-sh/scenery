package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lib/pq"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
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

func TestManagedPostgresEnvExposesOnlyDatabaseURL(t *testing.T) {
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
		"DATABASE_URL=postgres://localhost/poison",
	}, nil)
	if err != nil {
		t.Fatalf("managedPostgresEnv returned error: %v", err)
	}
	for _, want := range []string{
		appDatabaseURLEnv + "=postgres://localhost/demo_session",
		"SCENERY_MANAGED_DATABASE_URL=postgres://localhost/demo_session",
		"SCENERY_MANAGED_DATABASE_NAME=demo_session",
	} {
		if !containsString(env, want) {
			t.Fatalf("managed env missing %q: %+v", want, env)
		}
	}
	if countEnvKey(env, legacyDatabaseURLEnv) != 0 {
		t.Fatalf("managed env must not expose %s: %+v", legacyDatabaseURLEnv, env)
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

func TestManagedPostgresEnvRequiresDatabaseURLForExternalOptOut(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
		}},
	}
	_, err := managedPostgresEnv(t.Context(), cfg, nil, []string{
		devPostgresExternalEnv + "=1",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires DatabaseURL") || !strings.Contains(err.Error(), "DATABASE_URL is ignored") {
		t.Fatalf("managedPostgresEnv external error = %v", err)
	}
}

func TestExternalPostgresDatabaseURLRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := externalPostgresDatabaseURL([]string{devPostgresExternalEnv + "=1"})
	if err == nil || !strings.Contains(err.Error(), "requires DatabaseURL") {
		t.Fatalf("externalPostgresDatabaseURL missing error = %v", err)
	}
}

func TestManagedPostgresAdminURLCanComeFromAgentSubstrate(t *testing.T) {
	prevReachable := managedPostgresAdminReachableFn
	prevVersionMatches := postgresAdminVersionMatchesFn
	managedPostgresAdminReachableFn = func(context.Context, string) bool { return true }
	postgresAdminVersionMatchesFn = func(context.Context, string, string) bool { return true }
	defer func() {
		managedPostgresAdminReachableFn = prevReachable
		postgresAdminVersionMatchesFn = prevVersionMatches
	}()

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

func TestManagedPostgresAgentAdminURLRejectsUnreachableSubstrate(t *testing.T) {
	prevReachable := managedPostgresAdminReachableFn
	prevVersionMatches := postgresAdminVersionMatchesFn
	managedPostgresAdminReachableFn = func(context.Context, string) bool { return false }
	postgresAdminVersionMatchesFn = func(context.Context, string, string) bool { return true }
	defer func() {
		managedPostgresAdminReachableFn = prevReachable
		postgresAdminVersionMatchesFn = prevVersionMatches
	}()

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
		URLs:     map[string]string{"admin": "postgres://127.0.0.1:1/postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	env := envWithManagedPostgresAgentAdminURL(ctx, []string{"A=1"}, client)
	if countEnvKey(env, devPostgresAdminURLEnv) != 0 {
		t.Fatalf("env should not include unreachable postgres admin URL: %+v", env)
	}
	if _, err := client.GetSubstrate(ctx, localagent.SubstratePostgres); !localagent.IsNotFound(err) {
		t.Fatalf("postgres substrate after stale rejection err=%v", err)
	}
}

func TestManagedPostgresEnvDoesNotWriteSessionKeysToGlobalSubstrate(t *testing.T) {
	prevReachable := managedPostgresAdminReachableFn
	prevVersionMatches := postgresAdminVersionMatchesFn
	prevEnsure := ensureManagedPostgresDatabaseFn
	managedPostgresAdminReachableFn = func(context.Context, string) bool { return true }
	postgresAdminVersionMatchesFn = func(context.Context, string, string) bool { return true }
	ensureManagedPostgresDatabaseFn = func(context.Context, string, string) error { return nil }
	defer func() {
		managedPostgresAdminReachableFn = prevReachable
		postgresAdminVersionMatchesFn = prevVersionMatches
		ensureManagedPostgresDatabaseFn = prevEnsure
	}()

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
		Status:   "ready",
		OwnerPID: os.Getpid(),
		URLs:     map[string]string{"admin": "postgres://localhost/postgres"},
		Endpoints: map[string]string{
			"version":   devPostgresDefaultVersion,
			"isolation": devPostgresDefaultIsolation,
		},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
		}},
	}
	env, err := managedPostgresEnv(ctx, cfg, &localagent.Session{SessionID: "review-a", BaseAppID: "demo"}, []string{"A=1"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(env, "SCENERY_MANAGED_DATABASE_NAME=demo_review_a") {
		t.Fatalf("managed env = %+v", env)
	}
	substrate, err := client.GetSubstrate(ctx, localagent.SubstratePostgres)
	if err != nil {
		t.Fatal(err)
	}
	for key := range substrate.URLs {
		if strings.HasPrefix(key, "session.") {
			t.Fatalf("postgres substrate URL contains session key %q: %+v", key, substrate.URLs)
		}
	}
	for key := range substrate.Endpoints {
		if strings.HasPrefix(key, "session.") {
			t.Fatalf("postgres substrate endpoint contains session key %q: %+v", key, substrate.Endpoints)
		}
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

	got, err := postgresDatabaseURL("postgres://scenery@/postgres?host=%2Ftmp%2Fscenery+pg&port=55432&sslmode=disable", "session")
	if err != nil {
		t.Fatalf("postgresDatabaseURL returned error: %v", err)
	}
	if got != "postgres://scenery@/session?host=%2Ftmp%2Fscenery+pg&port=55432&sslmode=disable" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestLocalPostgresAdminURLUsesUnixSocketQuery(t *testing.T) {
	t.Parallel()

	got := localPostgresAdminURL("/tmp/scenery pg", 55432)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse local admin URL: %v", err)
	}
	if parsed.Scheme != "postgres" || parsed.User.Username() != "scenery" || parsed.Host != "" || parsed.Path != "/postgres" {
		t.Fatalf("admin URL = %q", got)
	}
	if parsed.Query().Get("host") != "/tmp/scenery pg" || parsed.Query().Get("port") != "55432" || parsed.Query().Get("sslmode") != "disable" {
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
	if parsed.User.Username() != "scenery" {
		t.Fatalf("admin URL user = %q", parsed.User.Username())
	}
	if password, _ := parsed.User.Password(); password != "postgres" {
		t.Fatalf("admin URL password = %q", password)
	}
	if parsed.Host != "127.0.0.1:55432" || parsed.Path != "/postgres" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("admin URL = %q", got)
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

func TestIsPostgresDuplicateDatabaseRace(t *testing.T) {
	t.Parallel()

	for _, err := range []*pq.Error{{Code: "23505"}, {Code: "42P04"}} {
		if !isPostgresDuplicateDatabaseRace(err) {
			t.Fatalf("code %s should be treated as duplicate database race", err.Code)
		}
	}
	if !isPostgresDuplicateDatabaseRace(&pq.Error{Code: "XX000", Message: "tuple concurrently updated"}) {
		t.Fatal("concurrent catalog update should be treated as duplicate database race")
	}
	if isPostgresDuplicateDatabaseRace(&pq.Error{Code: "XX000", Message: "unrelated internal error"}) {
		t.Fatal("unrelated internal error should not be treated as duplicate database race")
	}
	if isPostgresDuplicateDatabaseRace(&pq.Error{Code: "42601"}) {
		t.Fatal("syntax error should not be treated as duplicate database race")
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
