package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/workers"
	sceneryruntime "scenery.sh/runtime"
)

func TestAppChildEnvForcesColorWhenRequested(t *testing.T) {
	t.Parallel()

	env := appChildEnv([]string{"A=1"}, true, "B=2")
	if !containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) missing CLICOLOR_FORCE=1", env)
	}
}

func TestAppChildEnvLeavesColorUnsetWhenDisabled(t *testing.T) {
	t.Parallel()

	env := appChildEnv([]string{"A=1"}, false, "B=2")
	if containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) unexpectedly added CLICOLOR_FORCE=1", env)
	}
}

func TestAppDatabaseAuthorityEnvRemovesLegacyDatabaseURLForManagedPostgres(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{
			Name: "demo",
			Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
				"postgres": {Kind: "postgres"},
			}},
		},
	}
	env := s.appDatabaseAuthorityEnv([]string{
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
		appDatabaseURLEnv + "=postgres://localhost/user",
		"OTHER=1",
	})
	if containsString(env, legacyDatabaseURLEnv+"=postgres://localhost/poison") {
		t.Fatalf("app database env leaked %s: %v", legacyDatabaseURLEnv, env)
	}
	if containsString(env, appDatabaseURLEnv+"=postgres://localhost/user") {
		t.Fatalf("app database env leaked stale %s: %v", appDatabaseURLEnv, env)
	}
	if !containsString(env, "OTHER=1") {
		t.Fatalf("app database env removed unrelated values: %v", env)
	}
}

func TestAppDatabaseAuthorityEnvKeepsOnlyDatabaseURLForExternalPostgres(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{
			Name: "demo",
			Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
				"postgres": {Kind: "postgres"},
			}},
		},
	}
	env := s.appDatabaseAuthorityEnv([]string{
		devPostgresExternalEnv + "=1",
		legacyDatabaseURLEnv + "=postgres://localhost/external",
		appDatabaseURLEnv + "=postgres://localhost/external",
	})
	if containsString(env, legacyDatabaseURLEnv+"=postgres://localhost/external") {
		t.Fatalf("external app database env leaked %s: %v", legacyDatabaseURLEnv, env)
	}
	if !containsString(env, appDatabaseURLEnv+"=postgres://localhost/external") {
		t.Fatalf("external app database env removed %s: %v", appDatabaseURLEnv, env)
	}
}

func TestSessionIdentityEnvUsesAgentSession(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{Name: "demo"},
		agentSession: &localagent.Session{
			SessionID:    "feature-a-123abc",
			BaseAppID:    "demo",
			RuntimeAppID: "demo--feature-a-123abc",
			AppRoot:      "/tmp/onlv-a",
			Branch:       "feature/a",
		},
	}
	env := s.sessionIdentityEnv()
	for _, want := range []string{
		"SCENERY_SESSION_ID=feature-a-123abc",
		"SCENERY_BASE_APP_ID=demo",
		"SCENERY_RUNTIME_APP_ID=demo--feature-a-123abc",
		"SCENERY_APP_ROOT_HASH=" + appRootHash("/tmp/onlv-a"),
		"SCENERY_BRANCH=feature/a",
		"SCENERY_WORKTREE=onlv-a",
	} {
		if !containsString(env, want) {
			t.Fatalf("sessionIdentityEnv() = %v, missing %q", env, want)
		}
	}
}

func TestSessionTemporalEnvUsesAgentSession(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{
			Name: "demo",
			Temporal: app.TemporalConfig{
				Enabled: true,
			},
		},
		status: devdash.AppRecord{ID: "demo"},
		agentSession: &localagent.Session{
			SessionID: "feature-a-123abc",
			BaseAppID: "demo",
		},
	}
	env := s.sessionTemporalEnv()
	for _, want := range []string{
		"SCENERY_TEMPORAL_TASK_QUEUE_PREFIX=scenery.demo.feature-a-123abc",
		"SCENERY_TEMPORAL_DEPLOYMENT_NAME=scenery-demo-feature-a-123abc",
		"SCENERY_BUILD_ID=feature-a-123abc",
	} {
		if !containsString(env, want) {
			t.Fatalf("sessionTemporalEnv() = %v, missing %q", env, want)
		}
	}
}

func TestSessionAuthEnvUsesRoutedSessionURLs(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{
			Name: "demo",
			Auth: app.AuthConfig{
				Enabled:             true,
				PublicAppURLEnv:     "APP_URL",
				APIBaseURLEnv:       "API_URL",
				AuthCookieDomainEnv: "COOKIE_DOMAIN",
			},
		},
		addr: "127.0.0.1:4000",
		agentSession: &localagent.Session{
			SessionID: "feature-a-123abc",
			Routes: map[string]string{
				localagent.RouteAPI: "http://api.feature-a-123abc.local.dev",
			},
		},
	}
	env := s.sessionAuthEnv()
	for _, want := range []string{
		"API_URL=http://api.feature-a-123abc.local.dev",
		"API_BASE_URL=http://api.feature-a-123abc.local.dev",
		"SCENERY_API_BASE_URL=http://api.feature-a-123abc.local.dev",
		"APP_URL=http://api.feature-a-123abc.local.dev",
		"PUBLIC_APP_URL=http://api.feature-a-123abc.local.dev",
		"SCENERY_PUBLIC_APP_URL=http://api.feature-a-123abc.local.dev",
		"COOKIE_DOMAIN=",
		"AUTH_COOKIE_DOMAIN=",
		"SCENERY_AUTH_COOKIE_DOMAIN=",
	} {
		if !containsString(env, want) {
			t.Fatalf("sessionAuthEnv() = %v, missing %q", env, want)
		}
	}
}

func TestAppStatusIncludesVisibleSessionRoutes(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		status: devdash.AppRecord{
			ID:        "demo",
			SessionID: "feature-a-123abc",
			Running:   true,
		},
		agentSession: &localagent.Session{
			SessionID: "feature-a-123abc",
			Routes: map[string]string{
				localagent.RouteAPI:       "https://api.feature-a-123abc.local.dev:9440/",
				localagent.RouteDashboard: "https://console.feature-a-123abc.local.dev:9440/",
				localagent.RouteGrafana:   "https://grafana.feature-a-123abc.local.dev:9440/",
				"web":                     "https://web.feature-a-123abc.local.dev:9440/",
				"victoria":                "https://victoria.feature-a-123abc.local.dev:9440/",
			},
			Aliases: map[string]string{
				localagent.RouteAPI:       "https://api.demo.localhost/",
				localagent.RouteDashboard: "https://console.demo.localhost/",
				"web":                     "https://demo.localhost/",
				"victoria":                "https://victoria.demo.localhost/",
			},
		},
	}
	status := s.appStatus()
	for _, name := range []string{localagent.RouteAPI, localagent.RouteDashboard, localagent.RouteGrafana, "web"} {
		if status.Routes[name] == "" {
			t.Fatalf("appStatus routes missing %q: %+v", name, status.Routes)
		}
	}
	if _, ok := status.Routes["victoria"]; ok {
		t.Fatalf("appStatus exposed victoria route: %+v", status.Routes)
	}
	for _, name := range []string{localagent.RouteAPI, localagent.RouteDashboard, "web"} {
		if status.Aliases[name] == "" {
			t.Fatalf("appStatus aliases missing %q: %+v", name, status.Aliases)
		}
	}
	if _, ok := status.Aliases["victoria"]; ok {
		t.Fatalf("appStatus exposed victoria alias: %+v", status.Aliases)
	}
}

func TestAppEnvWithDotEnvAddsMissingValuesWithoutOverridingProcessEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-file\nB=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := appEnvWithDotEnv([]string{"A=from-process"}, root)
	if err != nil {
		t.Fatalf("appEnvWithDotEnv: %v", err)
	}
	if !containsString(env, "A=from-process") {
		t.Fatalf("env missing process value: %v", env)
	}
	if containsString(env, "A=from-file") {
		t.Fatalf("env should not override process value: %v", env)
	}
	if !containsString(env, "B=2") {
		t.Fatalf("env missing .env value: %v", env)
	}
}

func TestAppEnvWithDotEnvCanLoadLocalOverride(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-env\nB=from-env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("B=from-local\nC=from-local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := appEnvWithDotEnv([]string{"A=from-process"}, root, ".env", ".env.local")
	if err != nil {
		t.Fatalf("appEnvWithDotEnv: %v", err)
	}
	if !containsString(env, "A=from-process") {
		t.Fatalf("env missing process value: %v", env)
	}
	if !containsString(env, "B=from-local") {
		t.Fatalf("env missing .env.local override: %v", env)
	}
	if !containsString(env, "C=from-local") {
		t.Fatalf("env missing .env.local value: %v", env)
	}
}

func TestAppEnvWithRequiredDotEnvFailsWhenMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := appEnvWithRequiredDotEnv(nil, root)
	if err == nil {
		t.Fatal("appEnvWithRequiredDotEnv returned nil error")
	}
	if !strings.Contains(err.Error(), "missing required local env file") || !strings.Contains(err.Error(), filepath.Join(root, ".env")) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateLocalSecretsFilesRequiresDotEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	err := validateLocalSecretsFiles(root)
	if err == nil {
		t.Fatal("validateLocalSecretsFiles returned nil error")
	}
	if !strings.Contains(err.Error(), "missing required local env file") {
		t.Fatalf("error = %v", err)
	}
}

func TestAppProcessEnvAllowsMissingDotEnvForProduction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env, err := appProcessEnv(root, app.Config{Name: "demo"}, "json", "production")
	if err != nil {
		t.Fatalf("appProcessEnv production returned error: %v", err)
	}
	if !containsString(env, "SCENERY_ENV=production") || !containsString(env, "SCENERY_RUNTIME_ENV=production") {
		t.Fatalf("production env missing markers: %v", env)
	}
}

func TestAppStartupExitErrorIncludesOutput(t *testing.T) {
	t.Parallel()

	output := &safeLineTail{limit: 10}
	output.Add("warning: something happened")
	output.Add("fatal: database missing")
	err := appStartupExitError(&runningApp{output: output}, os.ErrInvalid)
	for _, want := range []string{"scenery app exited during startup", os.ErrInvalid.Error(), "fatal: database missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

// Not parallel: asserts that a just-closed ephemeral port refuses connections,
// which races with concurrent tests binding 127.0.0.1:0.
func TestTCPAddrAcceptsConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if !tcpAddrAcceptsConnections(addr) {
		t.Fatalf("tcpAddrAcceptsConnections(%q) = false, want true", addr)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if tcpAddrAcceptsConnections(addr) {
		t.Fatalf("tcpAddrAcceptsConnections(%q) after close = true, want false", addr)
	}
}

func TestTemporalDevHelpers(t *testing.T) {
	t.Setenv(sceneryruntime.DefaultTemporalAddressEnv, "")

	host, port, err := splitTemporalAddress("127.0.0.1:7233")
	if err != nil {
		t.Fatalf("splitTemporalAddress: %v", err)
	}
	if host != "127.0.0.1" || port != 7233 {
		t.Fatalf("host/port = %s/%d", host, port)
	}
	if _, _, err := splitTemporalAddress("not-a-host-port"); err == nil {
		t.Fatal("expected invalid address error")
	}

	root := t.TempDir()
	if got, want := temporalLocalDBPath(root, ".scenery/temporal/dev.db"), filepath.Join(root, ".scenery/temporal/dev.db"); got != want {
		t.Fatalf("temporalLocalDBPath = %q, want %q", got, want)
	}

	cfg := app.TemporalConfig{
		Enabled:    true,
		AddressEnv: "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:  "orders",
		Local: app.TemporalLocalConfig{
			AutoStart:  true,
			DBFilename: ".scenery/temporal/dev.db",
		},
	}
	rtCfg := temporalRuntimeConfigFromApp(cfg)
	if !rtCfg.Enabled || rtCfg.AddressEnv != "CUSTOM_TEMPORAL_ADDRESS" || rtCfg.Namespace != "orders" || !rtCfg.Local.AutoStart {
		t.Fatalf("runtime temporal config = %+v", rtCfg)
	}

	server := &temporalDevServer{info: sceneryRuntimeInfoForTest()}
	env := server.Env()
	if !containsString(env, "CUSTOM_TEMPORAL_ADDRESS=127.0.0.1:7233") || !containsString(env, "TEMPORAL_NAMESPACE=orders") {
		t.Fatalf("temporal env = %+v", env)
	}
	if got := temporalUIUpstreamForConfig(app.Config{Name: "test"}); got != "127.0.0.1:8233" {
		t.Fatalf("temporal UI upstream = %q, want %q", got, "127.0.0.1:8233")
	}
}

func TestPrepareSessionAppBinaryUsesSessionStateRoot(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(t.TempDir(), ".scenery", "sessions", "review-a")
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "scenery-app-abcdef")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := prepareSessionAppBinary(&localagent.Session{StateRoot: stateRoot}, binary)
	if err != nil {
		t.Fatalf("prepareSessionAppBinary: %v", err)
	}
	if !strings.HasPrefix(got, filepath.Join(stateRoot, "run", "app")+string(filepath.Separator)) {
		t.Fatalf("session app binary = %q, want under state root %q", got, stateRoot)
	}
	if filepath.Base(got) != "scenery-app-abcdef" {
		t.Fatalf("session app binary base = %q", filepath.Base(got))
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("session app binary missing: %v", err)
	}
}

func TestPrepareSessionAppBinaryErrorsWhenSessionTargetBlocked(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(t.TempDir(), ".scenery", "sessions", "review-a")
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "scenery-app-abcdef")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(stateRoot, "run", "app", filepath.Base(binary))
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := prepareSessionAppBinary(&localagent.Session{StateRoot: stateRoot}, binary)
	if err == nil {
		t.Fatalf("prepareSessionAppBinary returned %q, want blocked target error", got)
	}
}

func TestDevDatabaseSetupRunsInitialAndSkipsUnchangedRebuild(t *testing.T) {
	root := writeSetupCommandFixture(t)
	_, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot: %v", err)
	}
	store := newFakeSeedStore()
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	var applyRuns int
	restoreExec := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error {
		applyRuns++
		return nil
	}, nil)
	defer restoreExec()

	s := &devSupervisor{root: root, cfg: cfg, status: devdash.AppRecord{ID: cfg.AppID()}}
	setup, shouldRun, err := s.nextDevDatabaseSetup(true)
	if err != nil || !shouldRun {
		t.Fatalf("initial nextDevDatabaseSetup shouldRun=%v err=%v", shouldRun, err)
	}
	if err := s.runDevDatabaseSetup(context.Background(), setup); err != nil {
		t.Fatalf("runDevDatabaseSetup initial: %v", err)
	}
	if applyRuns != 1 || len(store.applied) != 1 {
		t.Fatalf("applyRuns=%d applied=%+v", applyRuns, store.applied)
	}

	_, shouldRun, err = s.nextDevDatabaseSetup(false)
	if err != nil {
		t.Fatalf("rebuild nextDevDatabaseSetup: %v", err)
	}
	if shouldRun {
		t.Fatal("unchanged rebuild should skip database setup")
	}
	if applyRuns != 1 || len(store.applied) != 1 {
		t.Fatalf("unchanged rebuild reran setup: applyRuns=%d applied=%+v", applyRuns, store.applied)
	}
}

func TestDevDatabaseSetupRerunsWhenSeedChanges(t *testing.T) {
	root := writeSetupCommandFixture(t)
	_, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot: %v", err)
	}
	store := newFakeSeedStore()
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	var applyRuns int
	restoreExec := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error {
		applyRuns++
		return nil
	}, nil)
	defer restoreExec()

	s := &devSupervisor{root: root, cfg: cfg, status: devdash.AppRecord{ID: cfg.AppID()}}
	setup, shouldRun, err := s.nextDevDatabaseSetup(true)
	if err != nil || !shouldRun {
		t.Fatalf("initial nextDevDatabaseSetup shouldRun=%v err=%v", shouldRun, err)
	}
	if err := s.runDevDatabaseSetup(context.Background(), setup); err != nil {
		t.Fatalf("runDevDatabaseSetup initial: %v", err)
	}
	writeTestAppFile(t, root, "auth/db/seed.sql", "insert into scenery_auth.users(id) values ('changed-user');\n")

	setup, shouldRun, err = s.nextDevDatabaseSetup(false)
	if err != nil || !shouldRun {
		t.Fatalf("changed seed nextDevDatabaseSetup shouldRun=%v err=%v", shouldRun, err)
	}
	err = s.runDevDatabaseSetup(context.Background(), setup)
	if err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("changed seed runDevDatabaseSetup error = %v", err)
	}
	if applyRuns != 2 {
		t.Fatalf("applyRuns=%d, want 2", applyRuns)
	}
}

func TestDevDatabaseSetupRetriesAfterApplyFailure(t *testing.T) {
	root := writeSetupCommandFixture(t)
	_, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot: %v", err)
	}
	store := newFakeSeedStore()
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	var applyRuns int
	restoreExec := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error {
		applyRuns++
		return fmt.Errorf("apply failed")
	}, nil)
	defer restoreExec()

	s := &devSupervisor{root: root, cfg: cfg, status: devdash.AppRecord{ID: cfg.AppID()}}
	setup, shouldRun, err := s.nextDevDatabaseSetup(true)
	if err != nil || !shouldRun {
		t.Fatalf("initial nextDevDatabaseSetup shouldRun=%v err=%v", shouldRun, err)
	}
	if err := s.runDevDatabaseSetup(context.Background(), setup); err == nil || !strings.Contains(err.Error(), "apply failed") {
		t.Fatalf("runDevDatabaseSetup apply error = %v", err)
	}

	_, shouldRun, err = s.nextDevDatabaseSetup(false)
	if err != nil || !shouldRun {
		t.Fatalf("failed setup should be retried on rebuild: shouldRun=%v err=%v", shouldRun, err)
	}
	if applyRuns != 1 || len(store.applied) != 0 {
		t.Fatalf("applyRuns=%d applied=%+v", applyRuns, store.applied)
	}
}

func TestDevSetupUsesManagedDatabaseURLWithoutLegacyDatabaseURL(t *testing.T) {
	root := t.TempDir()
	t.Setenv(legacyDatabaseURLEnv, "postgres://localhost/poison")
	t.Setenv(appDatabaseURLEnv, "postgres://localhost/stale")
	t.Setenv(devPostgresAdminURLEnv, "postgres://localhost/postgres")

	prevEnsure := ensureManagedPostgresDatabaseFn
	defer func() { ensureManagedPostgresDatabaseFn = prevEnsure }()
	ensureManagedPostgresDatabaseFn = func(_ context.Context, adminURL, dbName string) error {
		if adminURL != "postgres://localhost/postgres" || dbName != "demo_session" {
			t.Fatalf("ensure managed postgres got adminURL=%q dbName=%q", adminURL, dbName)
		}
		return nil
	}

	s := &devSupervisor{
		root: root,
		cfg: app.Config{
			Name: "demo",
			Dev: app.DevConfig{
				Services: map[string]app.DevServiceConfig{
					"postgres": {Kind: "postgres"},
				},
				Setup: []string{
					`test "$DatabaseURL" = "postgres://localhost/demo_session" && test -z "$DATABASE_URL" && test "$SCENERY_MANAGED_DATABASE_NAME" = "demo_session"`,
				},
			},
		},
		status: devdash.AppRecord{ID: "demo"},
		agentSession: &localagent.Session{
			SessionID: "session",
			BaseAppID: "demo",
		},
	}
	if err := s.runDevSetup(context.Background()); err != nil {
		t.Fatalf("runDevSetup: %v", err)
	}
}

func TestManagedAppEnvUsesReadyPostgresBranchLease(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv(devPostgresAdminURLEnv, "postgres://scenery:secret@127.0.0.1:55435/postgres?sslmode=disable")
	root := t.TempDir()
	s := &devSupervisor{
		root: root,
		cfg: app.Config{
			Name: "demo",
			ID:   "demo",
			Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
				"postgres": {
					Kind:               "postgres",
					Mode:               "local",
					Isolation:          "database",
					BranchStrategy:     "template_database",
					Project:            "demo",
					ParentDatabase:     "demo_main",
					BranchPolicy:       "session",
					BranchNameTemplate: "{app}/{session}",
				},
			}},
		},
		status: devdash.AppRecord{ID: "demo"},
		agentSession: &localagent.Session{
			SessionID: "review-a",
			BaseAppID: "demo",
		},
	}
	env, err := s.managedAppEnv(context.Background(), []string{"A=1"})
	if err == nil || !strings.Contains(err.Error(), "branch connection is not ready") {
		t.Fatalf("managedAppEnv env=%v err=%v", env, err)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read pin ok=%v err=%v", ok, err)
	}
	if pin.Branch != "demo/review-a" || pin.SessionID != "review-a" {
		t.Fatalf("pin = %+v", pin)
	}
	if err := upsertPostgresBranchLease(pin, &dbBranchEndpoint{
		Host:     "127.0.0.1",
		Port:     55435,
		Database: pin.Database,
		Role:     "scenery",
		SSLMode:  "disable",
		Source:   "postgres",
	}, "ready"); err != nil {
		t.Fatalf("upsert ready lease: %v", err)
	}
	env, err = s.managedAppEnv(context.Background(), []string{"A=1", appDatabaseURLEnv + "=postgres://localhost/stale", legacyDatabaseURLEnv + "=postgres://localhost/poison"})
	if err != nil {
		t.Fatalf("managedAppEnv ready: %v", err)
	}
	for _, want := range []string{
		appDatabaseURLEnv + "=postgres://scenery:secret@127.0.0.1:55435/demo_demo_review_a?sslmode=disable",
		"SCENERY_MANAGED_DATABASE_URL=postgres://scenery:secret@127.0.0.1:55435/demo_demo_review_a?sslmode=disable",
		"SCENERY_MANAGED_DATABASE_NAME=demo_demo_review_a",
	} {
		if !containsString(env, want) {
			t.Fatalf("managed env missing %q: %+v", want, env)
		}
	}
}

func TestManagedAppEnvUsesConfiguredPostgresBranchDatabaseURLEnv(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv(devPostgresAdminURLEnv, "postgres://scenery:secret@127.0.0.1:55435/postgres?sslmode=disable")
	root := t.TempDir()
	s := &devSupervisor{
		root: root,
		cfg: app.Config{
			Name: "demo",
			ID:   "demo",
			Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
				"postgres": {
					Kind:           "postgres",
					Mode:           "local",
					Isolation:      "database",
					BranchStrategy: "template_database",
					Project:        "demo",
					ParentDatabase: "demo_main",
					DatabaseURLEnv: "APP_DATABASE_URL",
				},
			}},
		},
		status: devdash.AppRecord{ID: "demo"},
		agentSession: &localagent.Session{
			SessionID: "review-a",
			BaseAppID: "demo",
			Branch:    "feature-a",
		},
	}
	_, err := s.managedAppEnv(context.Background(), nil)
	if err == nil {
		t.Fatal("managedAppEnv should require a ready Postgres branch lease")
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read pin ok=%v err=%v", ok, err)
	}
	if err := upsertPostgresBranchLease(pin, &dbBranchEndpoint{
		Host:     "127.0.0.1",
		Port:     55435,
		Database: pin.Database,
		Role:     "scenery",
		SSLMode:  "disable",
		Source:   "postgres",
	}, "ready"); err != nil {
		t.Fatalf("upsert ready lease: %v", err)
	}
	env, err := s.managedAppEnv(context.Background(), []string{
		"APP_DATABASE_URL=postgres://localhost/stale",
		appDatabaseURLEnv + "=postgres://localhost/stale-default",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	})
	if err != nil {
		t.Fatalf("managedAppEnv ready: %v", err)
	}
	if !containsString(env, "APP_DATABASE_URL=postgres://scenery:secret@127.0.0.1:55435/demo_demo_feature_a?sslmode=disable") {
		t.Fatalf("managed env missing configured database URL env: %+v", env)
	}
	if containsString(env, appDatabaseURLEnv+"=postgres://scenery:secret@127.0.0.1:55435/demo_demo_feature_a?sslmode=disable") || containsString(env, legacyDatabaseURLEnv+"=postgres://localhost/poison") {
		t.Fatalf("managed env leaked unconfigured database URL env: %+v", env)
	}
	appBaseEnv := s.appDatabaseAuthorityEnv([]string{
		"APP_DATABASE_URL=postgres://localhost/stale",
		appDatabaseURLEnv + "=postgres://localhost/stale-default",
		legacyDatabaseURLEnv + "=postgres://localhost/poison",
	})
	for _, key := range []string{"APP_DATABASE_URL=", appDatabaseURLEnv + "=", legacyDatabaseURLEnv + "="} {
		if envValueFromList(appBaseEnv, strings.TrimSuffix(key, "=")) != "" {
			t.Fatalf("app base env kept stale %s: %+v", key, appBaseEnv)
		}
	}
	setupEnv := managedDatabaseSetupEnv(s.cfg, env)
	if !containsString(setupEnv, appDatabaseURLEnv+"=postgres://scenery:secret@127.0.0.1:55435/demo_demo_feature_a?sslmode=disable") {
		t.Fatalf("setup env missing canonical database URL: %+v", setupEnv)
	}
}

func TestManagedAppEnvSkipsBranchingWhenExternalPostgresIsConfigured(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	s := &devSupervisor{
		root: root,
		cfg: app.Config{
			Name: "demo",
			Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
				"postgres": {Kind: "postgres", Mode: "local", Isolation: "database", BranchStrategy: "template_database", Project: "demo"},
			}},
		},
		status:       devdash.AppRecord{ID: "demo"},
		agentSession: &localagent.Session{SessionID: "review-a", BaseAppID: "demo"},
	}
	env, err := s.managedAppEnv(context.Background(), []string{
		devPostgresExternalEnv + "=1",
		appDatabaseURLEnv + "=postgres://localhost/external",
	})
	if err != nil {
		t.Fatalf("managedAppEnv external: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("managedAppEnv external env = %+v", env)
	}
	if _, ok, err := readWorktreeDBPin(worktreeDBPinPath(root)); err != nil || ok {
		t.Fatalf("external mode wrote branch pin ok=%v err=%v", ok, err)
	}
}

func TestTypeScriptWorkerAutoStartRequiresTemporalEnabled(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Temporal: app.TemporalConfig{
			TypeScript: app.TemporalTypeScript{
				Enabled:   true,
				AutoStart: true,
			},
		},
	}
	ts := workers.TypeScriptWorkerModel{Activities: []workers.TypeScriptActivity{{
		Name:      "house.RenderRoofPreview/v1",
		TaskQueue: "onlv.house.preview.ts",
	}}}

	got := effectiveDevConfigForTypeScriptWorker(cfg, ts)
	if got.Temporal.Enabled || got.Temporal.Local.AutoStart {
		t.Fatalf("TypeScript Temporal auto-start enabled temporal without explicit opt-in: %+v", got.Temporal)
	}
}

func TestTypeScriptWorkerAutoStartEnablesTemporalDevServerWhenExplicit(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Temporal: app.TemporalConfig{
			Enabled: true,
			TypeScript: app.TemporalTypeScript{
				Enabled:   true,
				AutoStart: true,
			},
		},
	}
	ts := workers.TypeScriptWorkerModel{Activities: []workers.TypeScriptActivity{{
		Name:      "house.RenderRoofPreview/v1",
		TaskQueue: "onlv.house.preview.ts",
	}}}

	got := effectiveDevConfigForTypeScriptWorker(cfg, ts)
	if !got.Temporal.Enabled || got.Temporal.Mode != "local" || !got.Temporal.Local.AutoStart {
		t.Fatalf("effective TypeScript Temporal dev config = %+v", got.Temporal)
	}
}

func TestTypeScriptWorkerAutoStartRequiresActivity(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Temporal: app.TemporalConfig{
			Enabled: true,
			TypeScript: app.TemporalTypeScript{
				Enabled:   true,
				AutoStart: true,
			},
		},
	}
	if typeScriptWorkerAutoStartEnabled(cfg, workers.TypeScriptWorkerModel{}) {
		t.Fatal("typeScriptWorkerAutoStartEnabled returned true without activities")
	}
}

func TestTypeScriptWorkerEnvUsesTemporalAndSessionOverrides(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		root: "/tmp/onlv-demo",
		cfg: app.Config{
			Name: "demo",
			Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
				"postgres": {Kind: "postgres"},
			}},
			Temporal: app.TemporalConfig{
				Enabled: true,
			},
		},
		status:   devdash.AppRecord{ID: "demo"},
		temporal: &temporalDevServer{info: sceneryRuntimeInfoForTest()},
		agentSession: &localagent.Session{
			SessionID:    "feature-a-123abc",
			BaseAppID:    "demo",
			RuntimeAppID: "demo--feature-a-123abc",
			AppRoot:      "/tmp/onlv-demo",
			Branch:       "feature/a",
		},
	}

	env := s.typeScriptWorkerEnv(
		[]string{
			"TEMPORAL_ADDRESS=old:7233",
			"SCENERY_BUILD_ID=old-build",
			legacyDatabaseURLEnv + "=postgres://localhost/poison",
			appDatabaseURLEnv + "=postgres://localhost/stale",
		},
		[]string{
			appDatabaseURLEnv + "=postgres://localhost/managed",
			"SCENERY_MANAGED_DATABASE_NAME=demo_session",
		},
	)
	for _, want := range []string{
		"TEMPORAL_ADDRESS=127.0.0.1:7233",
		"TEMPORAL_NAMESPACE=orders",
		"SCENERY_APP_ID=demo",
		"SCENERY_APP_ROOT=/tmp/onlv-demo",
		"SCENERY_ROLE=typescript-worker",
		fmt.Sprintf("SCENERY_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"SCENERY_TEMPORAL_TASK_QUEUE_PREFIX=scenery.demo.feature-a-123abc",
		"SCENERY_TEMPORAL_DEPLOYMENT_NAME=scenery-demo-feature-a-123abc",
		"SCENERY_BUILD_ID=feature-a-123abc",
		"SCENERY_SESSION_ID=feature-a-123abc",
		appDatabaseURLEnv + "=postgres://localhost/managed",
		"SCENERY_MANAGED_DATABASE_NAME=demo_session",
	} {
		if !containsString(env, want) {
			t.Fatalf("typeScriptWorkerEnv() = %v, missing %q", env, want)
		}
	}
	if countEnvKey(env, "SCENERY_BUILD_ID") != 1 || countEnvKey(env, "TEMPORAL_ADDRESS") != 1 || countEnvKey(env, appDatabaseURLEnv) != 1 {
		t.Fatalf("typeScriptWorkerEnv() has duplicate overrides: %v", env)
	}
	if countEnvKey(env, legacyDatabaseURLEnv) != 0 {
		t.Fatalf("typeScriptWorkerEnv() leaked %s: %v", legacyDatabaseURLEnv, env)
	}
}

func TestCompactEnvOverridesKeepsLastValue(t *testing.T) {
	t.Parallel()

	got := compactEnvOverrides([]string{"A=1", "B=2", "A=3"})
	want := []string{"B=2", "A=3"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("compactEnvOverrides() = %v, want %v", got, want)
	}
}

func TestTemporalSubstrateRoundTrip(t *testing.T) {
	t.Parallel()

	server := &temporalDevServer{
		info:   sceneryRuntimeInfoForTest(),
		uiURL:  "http://127.0.0.1:8233",
		dbPath: filepath.Join(t.TempDir(), "temporal.db"),
	}
	req := server.SubstrateRequest(123)
	if req.Kind != localagent.SubstrateTemporal || req.OwnerPID != 123 {
		t.Fatalf("substrate request = %+v", req)
	}
	if req.Endpoints["address"] != "127.0.0.1:7233" || req.Endpoints["namespace"] != "orders" {
		t.Fatalf("substrate endpoints = %+v", req.Endpoints)
	}
	if req.URLs["ui"] != "http://127.0.0.1:8233" {
		t.Fatalf("substrate urls = %+v", req.URLs)
	}

	restored := temporalDevServerFromSubstrate(localagent.Substrate{
		Kind:      localagent.SubstrateTemporal,
		URLs:      req.URLs,
		Endpoints: req.Endpoints,
	}, "orders", app.TemporalConfig{
		Enabled:    true,
		AddressEnv: "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:  "default",
		Local: app.TemporalLocalConfig{
			AutoStart: true,
		},
	})
	if restored == nil {
		t.Fatal("restored temporal server is nil")
		return
	}
	if !restored.external || restored.info.Address != "127.0.0.1:7233" || restored.info.Namespace != "orders" {
		t.Fatalf("restored server = %+v", restored)
	}
	if restored.URL() != "http://127.0.0.1:8233" {
		t.Fatalf("restored UI URL = %q", restored.URL())
	}
}

func countEnvKey(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}

func TestFrontendURLsFromAgentRoutes(t *testing.T) {
	t.Parallel()

	urls := frontendURLsFromAgentRoutes(map[string]string{
		localagent.RouteAPI:       "http://api.session.demo.localhost",
		localagent.RouteDashboard: "http://console.session.demo.localhost",
		localagent.RouteGrafana:   "http://grafana.session.demo.localhost",
		"web":                     "http://web.session.demo.localhost",
		"blog":                    "http://blog.session.demo.localhost",
		"electric":                "http://electric.session.demo.localhost",
		localagent.RouteTemporal:  "http://temporal.session.demo.localhost",
	}, map[string]app.FrontendConfig{"web": {}, "blog": {}})
	if len(urls) != 2 {
		t.Fatalf("frontend urls = %+v", urls)
	}
	if urls["web"] != "http://web.session.demo.localhost" || urls["blog"] != "http://blog.session.demo.localhost" {
		t.Fatalf("frontend urls = %+v", urls)
	}
}

func TestTemporalURLUsesAgentRoute(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		agentSession: &localagent.Session{Routes: map[string]string{
			localagent.RouteTemporal: "http://temporal.session.demo.localhost",
		}},
		temporal: &temporalDevServer{info: sceneryRuntimeInfoForTest()},
	}
	if got, want := s.temporalURL(), "http://temporal.session.demo.localhost"; got != want {
		t.Fatalf("temporalURL() = %q, want %q", got, want)
	}
}

func TestAgentTemporalDevServerRejectsDeadOwnerSubstrate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(runCtx) }()
	defer stopAgentServerForTest(t, cancel, done)

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateTemporal,
		Status:   "ready",
		OwnerPID: 999999,
		Endpoints: map[string]string{
			"address":   "127.0.0.1:7233",
			"namespace": "default",
		},
		URLs: map[string]string{"ui": "http://127.0.0.1:8233"},
	}); err != nil {
		t.Fatal(err)
	}
	s := &devSupervisor{
		agent: client,
		cfg: app.Config{
			Name: "demo",
			Temporal: app.TemporalConfig{
				Enabled: true,
				Local:   app.TemporalLocalConfig{AutoStart: true},
			},
		},
	}
	if temporal := s.agentTemporalDevServer(ctx); temporal != nil {
		t.Fatalf("agentTemporalDevServer reused stale substrate: %+v", temporal)
	}
	if _, err := client.GetSubstrate(ctx, localagent.SubstrateTemporal); !localagent.IsNotFound(err) {
		t.Fatalf("temporal substrate after stale rejection err=%v", err)
	}
}

func TestAgentVictoriaStackRejectsClosedListenerSubstrate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(runCtx) }()
	defer stopAgentServerForTest(t, cancel, done)

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{}
	endpoints := map[string]string{}
	pids := map[string]int{}
	for _, spec := range victoriaComponentSpecs() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		urls[spec.Name] = baseURL
		endpoints[spec.Name] = baseURL + spec.EndpointPath
		pids[spec.Name] = os.Getpid()
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:      localagent.SubstrateVictoria,
		Status:    "ready",
		OwnerPID:  os.Getpid(),
		PIDs:      pids,
		URLs:      urls,
		Endpoints: endpoints,
	}); err != nil {
		t.Fatal(err)
	}
	s := &devSupervisor{agent: client}
	if stack := s.agentVictoriaStack(ctx); stack != nil {
		t.Fatalf("agentVictoriaStack reused closed listener substrate: %+v", stack)
	}
	if _, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria); !localagent.IsNotFound(err) {
		t.Fatalf("victoria substrate after closed listener rejection err=%v", err)
	}
}

func TestMonitorSharedTemporalPersistsExitState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(runCtx) }()
	defer stopAgentServerForTest(t, cancel, done)

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	temporal := &temporalDevServer{
		done:      make(chan error, 1),
		info:      sceneryRuntimeInfoForTest(),
		uiURL:     "http://127.0.0.1:8233",
		stdoutLog: "/tmp/temporal.stdout.log",
		stderrLog: "/tmp/temporal.stderr.log",
		startedAt: time.Now().Add(-time.Second).UTC(),
	}
	s := &devSupervisor{agent: client, cfg: app.Config{Name: "demo"}}
	monitorDone := s.monitorSharedTemporalDevServer(temporal)
	temporal.done <- fmt.Errorf("exit status 2")
	close(temporal.done)

	substrate := waitForSubstrateStatus(t, ctx, client, localagent.SubstrateTemporal, "exited")
	if substrate.LastExit == nil || substrate.LastExit.Component != "server" || substrate.LastExit.StderrLogPath == "" {
		t.Fatalf("temporal exit substrate = %+v", substrate)
	}
	waitForMonitorDone(t, monitorDone)
}

func TestMonitorSharedVictoriaPersistsComponentExitState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(runCtx) }()
	defer stopAgentServerForTest(t, cancel, done)

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	spec := victoriaComponentSpecs()[0]
	component := &victoriaComponent{
		spec:        spec,
		baseURL:     "http://127.0.0.1:8428",
		endpointURL: "http://127.0.0.1:8428" + spec.EndpointPath,
		stdoutLog:   "/tmp/victoria.stdout.log",
		stderrLog:   "/tmp/victoria.stderr.log",
		done:        make(chan error, 1),
		startedAt:   time.Now().Add(-time.Second).UTC(),
	}
	stack := &victoriaStack{components: []*victoriaComponent{component}}
	s := &devSupervisor{agent: client, cfg: app.Config{Name: "demo"}}
	monitorDone := s.monitorSharedVictoriaStack(stack)
	component.done <- fmt.Errorf("exit status 9")
	close(component.done)

	substrate := waitForSubstrateStatus(t, ctx, client, localagent.SubstrateVictoria, "degraded")
	if substrate.LastExit == nil || substrate.ComponentExits[spec.Name].Component != spec.Name {
		t.Fatalf("victoria exit substrate = %+v", substrate)
	}
	waitForMonitorDone(t, monitorDone)
}

func stopAgentServerForTest(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("agent shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func waitForSubstrateStatus(t *testing.T, ctx context.Context, client *localagent.Client, kind, status string) localagent.Substrate {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last localagent.Substrate
	var lastErr error
	for time.Now().Before(deadline) {
		got, err := client.GetSubstrate(ctx, kind)
		if err == nil {
			last = got
			if got.Status == status {
				return got
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("substrate %s status = %+v err=%v, want %s", kind, last, lastErr, status)
	return localagent.Substrate{}
}

func waitForMonitorDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for substrate monitor")
	}
}

func TestBackendFromHTTPURL(t *testing.T) {
	t.Parallel()

	got := backendFromHTTPURL("http://127.0.0.1:10429")
	if got.Network != "tcp" || got.Addr != "127.0.0.1:10429" {
		t.Fatalf("backendFromHTTPURL() = %+v", got)
	}
}

func TestDevReportURLUsesLocalDashboardReportEndpoint(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		agentSession: &localagent.Session{
			Routes: map[string]string{
				localagent.RouteDashboard: "http://console.session.demo.localhost:4100",
			},
		},
	}
	if got, want := s.devReportURL(), "http://127.0.0.1:9401/__scenery/report"; got != want {
		t.Fatalf("devReportURL() = %q, want %q", got, want)
	}
}

func TestDevReportURLUsesAgentDashboardBackend(t *testing.T) {
	t.Parallel()

	server, err := localagent.NewServer(localagent.RunOptions{
		Home:       t.TempDir(),
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:45678",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	}()

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	s := &devSupervisor{agent: client}
	if got, want := s.devReportURL(), "http://127.0.0.1:45678/__scenery/report"; got != want {
		t.Fatalf("devReportURL() = %q, want %q", got, want)
	}
}

func sceneryRuntimeInfoForTest() sceneryruntime.TemporalRuntimeInfo {
	return sceneryruntime.TemporalRuntimeInfo{
		Enabled:         true,
		Address:         "127.0.0.1:7233",
		AddressEnv:      "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:       "orders",
		TaskQueuePrefix: "scenery.orders",
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()

	input := []byte("\x1b[34mTRC\x1b[0m request completed code=ok\n")
	got := stripANSI(input)
	want := []byte("TRC request completed code=ok\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("stripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestIsExpectedOutputReadError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "eof", err: io.EOF, want: true},
		{name: "os err closed", err: os.ErrClosed, want: true},
		{name: "net err closed", err: net.ErrClosed, want: true},
		{name: "wrapped path error", err: &fs.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}, want: true},
		{name: "other", err: io.ErrUnexpectedEOF, want: false},
	}
	for _, tt := range tests {
		if got := isExpectedOutputReadError(tt.err); got != tt.want {
			t.Fatalf("%s: isExpectedOutputReadError(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

func TestListAppsReturnsOnlyActiveSupervisorApp(t *testing.T) {
	t.Parallel()

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	for _, rec := range []devdash.AppRecord{
		{ID: "basicapp", Name: "basicapp", Root: "/tmp/basicapp", UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "cronapp", Name: "cronapp", Root: "/tmp/cronapp", UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "demoapp-dev", Name: "demoapp", Root: "/tmp/demoapp", Running: true, UpdatedAt: now},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	s := &devSupervisor{
		cfg:   app.Config{Name: "demoapp", ID: "demoapp-dev"},
		store: store,
		status: devdash.AppRecord{
			ID:      "demoapp-dev",
			Name:    "demoapp",
			Root:    "/tmp/demoapp",
			Running: true,
		},
	}

	items, err := s.listApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("listApps() returned %d items, want 1: %#v", len(items), items)
	}
	if got := items[0]["id"]; got != "demoapp-dev" {
		t.Fatalf("listApps()[0].id = %v, want demoapp-dev", got)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestLooksLikeSceneryDashboardProcess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info procInfo
		want bool
	}{
		{
			name: "scenery up process",
			info: procInfo{pid: 100, ppid: 1, cmd: "/usr/local/bin/scenery up"},
			want: true,
		},
		{
			name: "non orphaned scenery up process",
			info: procInfo{pid: 100, ppid: 42, cmd: "/usr/local/bin/scenery up"},
			want: true,
		},
		{
			name: "scenery serve is headless",
			info: procInfo{pid: 100, ppid: 42, cmd: "/usr/local/bin/scenery serve"},
			want: false,
		},
		{
			name: "scenery app binary is not dashboard",
			info: procInfo{pid: 100, ppid: 42, cmd: "/tmp/scenery-app"},
			want: false,
		},
		{
			name: "non scenery process",
			info: procInfo{pid: 100, ppid: 1, cmd: "/usr/bin/python3 -m http.server"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeSceneryDashboardProcess(tt.info); got != tt.want {
				t.Fatalf("looksLikeSceneryDashboardProcess(%+v) = %v, want %v", tt.info, got, tt.want)
			}
		})
	}
}
