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
		"ONLAVA_MANAGED_DATABASE_URL=postgres://localhost/demo_session",
		"ONLAVA_MANAGED_DATABASE_NAME=demo_session",
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
	if !containsString(env, "ONLAVA_MANAGED_DATABASE_NAME=demo_review_a") {
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

func TestResolveLocalPostgresBinariesFindsExplicitSibling(t *testing.T) {
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
		t.Fatalf("resolveLocalPostgresBinaries should not search PATH for %s", file)
		return "", os.ErrNotExist
	}
	binaries, err := resolveLocalPostgresBinaries([]string{devPostgresInitDBEnv + "=" + initdb})
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
		"electric": "http://electric.session.demo.localhost",
	}}, baseEnv)
	if err != nil {
		t.Fatalf("managedElectricEnv returned error: %v", err)
	}
	if !containsString(env, "ELECTRIC_URL=http://electric.session.demo.localhost") {
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

func TestManagedElectricProcessEnvIncludesSessionIdentity(t *testing.T) {
	t.Parallel()

	plan := &managedElectricPlan{}
	env := managedElectricProcessEnv(plan, []string{"PATH=/bin"}, "postgres://session/db", 3456, "onlava_session",
		"ONLAVA_APP_ROOT=/repo/app",
		"ONLAVA_SESSION_ID=main-dbe32e",
		"ONLAVA_BASE_APP_ID=demo",
		"ONLAVA_RUNTIME_APP_ID=demo--main-dbe32e",
		"ONLAVA_DEV_SUPERVISOR=1",
		"ONLAVA_ROLE=electric",
	)
	for _, want := range []string{
		"ONLAVA_APP_ROOT=/repo/app",
		"ONLAVA_SESSION_ID=main-dbe32e",
		"ONLAVA_BASE_APP_ID=demo",
		"ONLAVA_RUNTIME_APP_ID=demo--main-dbe32e",
		"ONLAVA_DEV_SUPERVISOR=1",
		"ONLAVA_ROLE=electric",
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

func TestManagedElectricSlotDiagnostics(t *testing.T) {
	t.Parallel()

	slot := managedElectricSlotName("onlava_main_dbe32e")
	if slot != "electric_slot_onlava_main_dbe32e" {
		t.Fatalf("slot = %q", slot)
	}
	got := describeElectricPostgresLocks([]electricPostgresLock{{
		PID:           123,
		Kind:          "advisory-lock",
		State:         "active",
		WaitEventType: "Lock",
		WaitEvent:     "advisory",
		Query:         "SELECT pg_advisory_lock(hashtext('electric_slot_onlava_main_dbe32e'))",
		Application:   "onlava-electric:root:session:runtime:stream",
		ClientAddr:    "127.0.0.1",
		SlotName:      slot,
	}})
	for _, want := range []string{"pid=123", "kind=advisory-lock", "wait=Lock/advisory", "electric_slot_onlava_main_dbe32e", "application=", "client=127.0.0.1", "slot="} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostic %q missing %q", got, want)
		}
	}
}

func TestManagedElectricPostgresApplicationNameIsScopedAndURLSafe(t *testing.T) {
	t.Parallel()

	root := "/repo/app"
	session := &localagent.Session{SessionID: "main-dbe32e", BaseAppID: "demo", RuntimeAppID: "demo--main-dbe32e"}
	first := managedElectricPostgresApplicationName(root, session, "onlava_main_dbe32e")
	second := managedElectricPostgresApplicationName(root, session, "onlava_main_dbe32e")
	otherRoot := managedElectricPostgresApplicationName("/repo/other", session, "onlava_main_dbe32e")
	otherRuntime := managedElectricPostgresApplicationName(root, &localagent.Session{SessionID: "main-dbe32e", BaseAppID: "demo", RuntimeAppID: "demo--other"}, "onlava_main_dbe32e")
	if first == "" || first != second {
		t.Fatalf("application name unstable: %q then %q", first, second)
	}
	if first == otherRoot || first == otherRuntime {
		t.Fatalf("application name not scoped: first=%q otherRoot=%q otherRuntime=%q", first, otherRoot, otherRuntime)
	}
	got := postgresURLWithApplicationName("postgres://localhost/session?sslmode=disable", first)
	if !strings.Contains(got, "application_name=onlava-electric%3A") || !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("postgres URL with application_name = %q", got)
	}
}

func TestCleanupManagedElectricStreamProcessesStopsSameSessionProcess(t *testing.T) {
	root := t.TempDir()
	stale := startSleepProcessForCleanupTest(t)
	defer func() {
		_ = killProcessTree(stale)
		_, _ = stale.Process.Wait()
	}()
	restore := listManagedElectricStreamProcesses
	listManagedElectricStreamProcesses = func(streamID string) ([]managedElectricStreamProcess, error) {
		if streamID != "onlava_main_dbe32e" {
			t.Fatalf("streamID = %q", streamID)
		}
		return []managedElectricStreamProcess{{
			PID:     stale.Process.Pid,
			State:   "S",
			Stream:  streamID,
			Command: "docker run -e ELECTRIC_REPLICATION_STREAM_ID=onlava_main_dbe32e -e ONLAVA_APP_ROOT=" + root + " -e ONLAVA_SESSION_ID=main-dbe32e -e ONLAVA_RUNTIME_APP_ID=demo--main-dbe32e -e ONLAVA_DEV_SUPERVISOR=1 -e ONLAVA_ROLE=electric electricsql/electric:canary",
		}}, nil
	}
	defer func() { listManagedElectricStreamProcesses = restore }()

	err := cleanupManagedElectricStreamProcesses(context.Background(), root, &localagent.Session{SessionID: "main-dbe32e", BaseAppID: "demo", RuntimeAppID: "demo--main-dbe32e"}, "onlava_main_dbe32e")
	if err != nil {
		t.Fatalf("cleanupManagedElectricStreamProcesses: %v", err)
	}
	waitForProcessExitForCleanupTest(t, stale.Process.Pid)
	_, _ = stale.Process.Wait()
}

func TestCleanupManagedElectricStreamProcessesFailsForLiveUnownedStream(t *testing.T) {
	restore := listManagedElectricStreamProcesses
	listManagedElectricStreamProcesses = func(streamID string) ([]managedElectricStreamProcess, error) {
		return []managedElectricStreamProcess{{
			PID:     424242,
			State:   "S",
			Stream:  streamID,
			Command: "docker run -e ELECTRIC_REPLICATION_STREAM_ID=" + streamID + " electricsql/electric:canary",
		}}, nil
	}
	defer func() { listManagedElectricStreamProcesses = restore }()

	err := cleanupManagedElectricStreamProcesses(context.Background(), t.TempDir(), &localagent.Session{SessionID: "main-dbe32e"}, "onlava_main_dbe32e")
	if err == nil || !strings.Contains(err.Error(), "pid=424242") || !strings.Contains(err.Error(), "onlava_main_dbe32e") {
		t.Fatalf("cleanupManagedElectricStreamProcesses error = %v", err)
	}
}

func TestCleanupManagedElectricStreamProcessesFailsForDifferentRuntimeSameStream(t *testing.T) {
	root := t.TempDir()
	restore := listManagedElectricStreamProcesses
	listManagedElectricStreamProcesses = func(streamID string) ([]managedElectricStreamProcess, error) {
		return []managedElectricStreamProcess{{
			PID:     424242,
			State:   "S",
			Stream:  streamID,
			Command: "docker run -e ELECTRIC_REPLICATION_STREAM_ID=" + streamID + " -e ONLAVA_APP_ROOT=" + root + " -e ONLAVA_SESSION_ID=main-dbe32e -e ONLAVA_RUNTIME_APP_ID=demo--other -e ONLAVA_DEV_SUPERVISOR=1 -e ONLAVA_ROLE=electric electricsql/electric:canary",
		}}, nil
	}
	defer func() { listManagedElectricStreamProcesses = restore }()

	err := cleanupManagedElectricStreamProcesses(context.Background(), root, &localagent.Session{SessionID: "main-dbe32e", BaseAppID: "demo", RuntimeAppID: "demo--main-dbe32e"}, "onlava_main_dbe32e")
	if err == nil || !strings.Contains(err.Error(), "demo--other") || !strings.Contains(err.Error(), "state=S") || !strings.Contains(err.Error(), `stream="onlava_main_dbe32e"`) {
		t.Fatalf("cleanupManagedElectricStreamProcesses error = %v", err)
	}
}

func TestCommandContainsEnvValueRequiresExactValue(t *testing.T) {
	t.Parallel()

	command := "docker run -e ELECTRIC_REPLICATION_STREAM_ID=onlava_main_dbe32e_other electricsql/electric:canary"
	if commandContainsEnvValue(command, "ELECTRIC_REPLICATION_STREAM_ID", "onlava_main_dbe32e") {
		t.Fatalf("command matched a stream id prefix: %q", command)
	}
}

func TestClassifyElectricPostgresLocksForCleanupUsesExactApplication(t *testing.T) {
	t.Parallel()

	ownedApp := "onlava-electric:root:session:runtime:stream"
	locks := []electricPostgresLock{
		{PID: 101, Application: ownedApp, SlotName: "electric_slot_onlava_main_dbe32e"},
		{PID: 202, Application: ownedApp + "-other", SlotName: "electric_slot_onlava_main_dbe32e"},
		{PID: 303, Application: "", SlotName: "electric_slot_onlava_main_dbe32e"},
	}
	owned, blocked := classifyElectricPostgresLocksForCleanup(locks, ownedApp)
	if len(owned) != 1 || owned[0].PID != 101 {
		t.Fatalf("owned locks = %+v", owned)
	}
	if len(blocked) != 2 || blocked[0].PID != 202 || blocked[1].PID != 303 {
		t.Fatalf("blocked locks = %+v", blocked)
	}
}

func TestTextMentionsExactIdentifier(t *testing.T) {
	t.Parallel()

	if !textMentionsExactIdentifier("SELECT pg_advisory_lock(hashtext('electric_slot_onlava_main_dbe32e'))", "electric_slot_onlava_main_dbe32e") {
		t.Fatal("expected exact slot mention")
	}
	if textMentionsExactIdentifier("SELECT 'electric_slot_onlava_main_dbe32e_other'", "electric_slot_onlava_main_dbe32e") {
		t.Fatal("slot prefix matched as exact identifier")
	}
}

func TestElectricPostgresLocksQueryCastsSlotParameterToText(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"$1::text AS slot_name",
		"a.query ILIKE '%' || $1::text || '%'",
		"s.slot_name::text",
		"s.slot_name::text = $1::text",
	} {
		if !strings.Contains(electricPostgresLocksQuery, want) {
			t.Fatalf("electricPostgresLocksQuery missing %q:\n%s", want, electricPostgresLocksQuery)
		}
	}
	for _, disallowed := range []string{
		"$1 AS slot_name",
		"|| $1 ||",
		"s.slot_name = $1",
	} {
		if strings.Contains(electricPostgresLocksQuery, disallowed) {
			t.Fatalf("electricPostgresLocksQuery contains uncast slot parameter %q:\n%s", disallowed, electricPostgresLocksQuery)
		}
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
	baseEnv := []string{
		devPostgresAdminURLEnv + "=postgres://localhost/postgres",
		appDatabaseURLEnv + "=postgres://localhost/explicit",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	}
	got, err := managedElectricDatabaseURL(t.Context(), t.TempDir(), cfg, &localagent.Session{
		SessionID: "session",
		BaseAppID: "demo",
	}, &managedElectricPlan{ServiceName: "electric"}, baseEnv, nil)
	if err != nil {
		t.Fatalf("managedElectricDatabaseURL returned error: %v", err)
	}
	if got != "postgres://localhost/demo_session" {
		t.Fatalf("database URL = %q", got)
	}
	env := managedElectricProcessEnv(&managedElectricPlan{
		Env: map[string]string{
			legacyDatabaseURLEnv: "$" + appDatabaseURLEnv,
		},
	}, baseEnv, got, 3456, "onlava_session")
	if !containsString(env, legacyDatabaseURLEnv+"=postgres://localhost/demo_session") {
		t.Fatalf("managed electric env missing private %s adapter: %+v", legacyDatabaseURLEnv, env)
	}
	if !containsString(env, appDatabaseURLEnv+"=postgres://localhost/demo_session") {
		t.Fatalf("managed electric env missing %s: %+v", appDatabaseURLEnv, env)
	}
	if containsString(env, legacyDatabaseURLEnv+"=postgres://localhost/poison") || countEnvKey(env, legacyDatabaseURLEnv) != 1 {
		t.Fatalf("managed electric env used stale %s: %+v", legacyDatabaseURLEnv, env)
	}
}

func TestManagedElectricDatabaseURLUsesReadyNeonBranch(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", Project: "demo"},
			"electric": {Kind: "electric"},
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
		Port:     55436,
		Database: "demo",
		Role:     "cloud_admin",
		SSLMode:  "disable",
	})
	got, err := managedElectricDatabaseURL(t.Context(), root, cfg, &localagent.Session{
		SessionID: "review-a",
		BaseAppID: "demo",
	}, &managedElectricPlan{ServiceName: "electric"}, []string{
		appDatabaseURLEnv + "=postgres://localhost/stale",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	}, nil)
	if err != nil {
		t.Fatalf("managedElectricDatabaseURL returned error: %v", err)
	}
	if got != "postgres://cloud_admin@127.0.0.1:55436/demo?sslmode=disable" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestManagedElectricDatabaseURLRequiresManagedResolutionWhenPostgresManaged(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
			"electric": {Kind: "electric"},
		}},
	}
	_, err := managedElectricDatabaseURL(t.Context(), t.TempDir(), cfg, &localagent.Session{
		SessionID: "session",
		BaseAppID: "demo",
	}, &managedElectricPlan{ServiceName: "electric"}, []string{
		appDatabaseURLEnv + "=postgres://localhost/explicit",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "managed dev.services.postgres") || !strings.Contains(err.Error(), devPostgresAdminURLEnv) {
		t.Fatalf("managedElectricDatabaseURL managed resolution error = %v", err)
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
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
		"DatabaseURL=postgres://localhost/explicit",
	}, nil)
	if err != nil {
		t.Fatalf("managedElectricDatabaseURL returned error: %v", err)
	}
	if got != "postgres://localhost/explicit" {
		t.Fatalf("database URL = %q", got)
	}
}

func TestManagedElectricDatabaseURLRejectsLegacyOnlyExternalPostgresOptOut(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "postgres"},
			"electric": {Kind: "electric"},
		}},
	}
	_, err := managedElectricDatabaseURL(t.Context(), t.TempDir(), cfg, &localagent.Session{
		SessionID: "session",
		BaseAppID: "demo",
	}, &managedElectricPlan{ServiceName: "electric"}, []string{
		devPostgresExternalEnv + "=1",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires DatabaseURL") || !strings.Contains(err.Error(), "DATABASE_URL is ignored") {
		t.Fatalf("managedElectricDatabaseURL external error = %v", err)
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
