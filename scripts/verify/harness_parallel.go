package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/victoria"
)

var runHarnessParallelDevCheckFunc = runHarnessParallelDevCheck

func runHarnessParallelDevStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "parallel worktree runtimes",
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"},
	}
	summary, diagnostics, err := runHarnessParallelDevCheckFunc(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = summary
	step.Diagnostics = diagnostics
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix local agent session isolation, then rerun `go run ./scripts/verify -o json`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessParallelDevCheck(parent context.Context, repoRoot string) (_ map[string]any, _ []checkDiagnostic, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	label := harnessRandomLabel()
	agentHome := filepath.Join(os.TempDir(), "scenery-harness-parallel-"+label)
	dockerAvailable := harnessDockerAvailable(ctx)
	var extraDiagnostics []checkDiagnostic
	if !dockerAvailable {
		extraDiagnostics = append(extraDiagnostics, checkDiagnostic{
			Stage:           "parallel worktree runtimes",
			Severity:        "warning",
			Message:         "Docker is unavailable; skipped managed Postgres database isolation checks",
			SuggestedAction: "Start Docker and rerun `go run ./scripts/verify -o json --write` for live database isolation proof.",
		})
	}
	restoreEnv := patchEnv(map[string]*string{
		"DATABASE_URL":                  nil,
		"SCENERY_AGENT_HOME":            stringPtr(agentHome),
		"SCENERY_DEV_CACHE_DIR":         nil,
		"SCENERY_DEV_DASHBOARD_ADDR":    nil,
		"SCENERY_DEV_VICTORIA":          stringPtr("0"),
		"SCENERY_DEV_VICTORIA_DOWNLOAD": stringPtr("0"),
	})
	defer restoreEnv()

	producer := cliProducer()
	server, err := localagent.NewServer(localagent.RunOptions{
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:9",
		},
		Identity: localagent.Identity{Version: producer.Version, Commit: producer.Commit, BuiltAt: producer.BuiltAt},
	})
	if err != nil {
		return nil, nil, err
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(serverCtx) }()
	defer func() {
		stopServer()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}()
	client := localagent.NewClient(server.Paths().SocketPath)
	if err := waitForHarnessAgent(ctx, client); err != nil {
		return nil, nil, err
	}

	frontendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("frontend-a")) }))
	defer frontendA.Close()
	frontendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("frontend-b")) }))
	defer frontendB.Close()
	observabilityAddr, closeObservability, err := reserveHarnessAddr()
	if err != nil {
		return nil, nil, err
	}
	defer closeObservability()
	root := filepath.Join(os.TempDir(), "scenery-harness-apps-"+harnessRandomLabel())
	rootA := filepath.Join(root, "worktree-a")
	rootB := filepath.Join(root, "worktree-b")
	defer func() {
		if returnErr == nil {
			_ = os.RemoveAll(root)
		}
	}()
	cfgA := harnessParallelConfig(frontendA.Listener.Addr().String())
	cfgB := harnessParallelConfig(frontendB.Listener.Addr().String())
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		for _, appRoot := range []string{rootA, rootB} {
			if pathExists(filepath.Join(appRoot, ".scenery.json")) {
				_, downErr := runHarnessAppCLI(cleanupCtx, repoRoot, appRoot, agentHome, "down", "-o", "json")
				returnErr = errors.Join(returnErr, downErr)
			}
		}
		cleanupErr := errors.Join(cleanupHarnessWorktreePostgres(cleanupCtx, rootA, cfgA.AppID()), cleanupHarnessWorktreePostgres(cleanupCtx, rootB, cfgB.AppID()))
		returnErr = errors.Join(returnErr, cleanupErr)
		if cleanupErr == nil {
			_ = os.RemoveAll(agentHome)
		}
	}()
	var (
		databaseEnvA, databaseEnvB []string
		databaseA, databaseB       postgresdb.Database
	)
	if dockerAvailable {
		for _, fixture := range []struct {
			root string
			cfg  app.Config
		}{{rootA, cfgA}, {rootB, cfgB}} {
			if err := copyHarnessBasicFixture(repoRoot, fixture.root); err != nil {
				return nil, nil, err
			}
			if err := writeHarnessFixtureConfig(fixture.root, fixture.cfg); err != nil {
				return nil, nil, err
			}
			if err := addHarnessSQLDeclarations(repoRoot, fixture.root, "main"); err != nil {
				return nil, nil, err
			}
		}
		databaseEnvA, databaseA, err = provisionHarnessDatabase(ctx, repoRoot, rootA, cfgA, "main")
		if err != nil {
			return nil, nil, err
		}
		databaseEnvB, databaseB, err = provisionHarnessDatabase(ctx, repoRoot, rootB, cfgB, "main")
		if err != nil {
			return nil, nil, err
		}
	}

	sessionA, err := prepareHarnessParallelSession(ctx, repoRoot, agentHome, rootA, cfgA)
	if err != nil {
		return nil, nil, err
	}
	sessionB, err := prepareHarnessParallelSession(ctx, repoRoot, agentHome, rootB, cfgB)
	if err != nil {
		return nil, nil, err
	}

	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateVictoria,
		Status:   "ready",
		OwnerPID: os.Getpid(),
		URLs: map[string]string{
			"metrics": "http://" + observabilityAddr,
			"logs":    "http://" + observabilityAddr,
			"traces":  "http://" + observabilityAddr,
		},
		Endpoints: map[string]string{
			"metrics": "http://" + observabilityAddr + "/opentelemetry/v1/metrics",
			"logs":    "http://" + observabilityAddr + "/insert/opentelemetry/v1/logs",
			"traces":  "http://" + observabilityAddr + "/insert/opentelemetry/v1/traces",
		},
	}); err != nil {
		return nil, nil, err
	}

	store, err := devdash.OpenStore(filepath.Join(server.Paths().AgentDir, "dashboard"))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = store.Close() }()
	if err := writeHarnessParallelObservability(ctx, store, cfgA.AppID(), sessionA, sessionB); err != nil {
		return nil, nil, err
	}

	diagnostics := validateHarnessParallelState(ctx, repoRoot, server, store, cfgA.AppID(), sessionA, sessionB, databaseEnvA, databaseEnvB, databaseA, databaseB, dockerAvailable)
	diagnostics = append(diagnostics, extraDiagnostics...)
	databaseCount := 0
	if dockerAvailable {
		databaseCount = 2
	}
	summary := map[string]any{
		"postgres_verified": dockerAvailable,
		"sessions":          2,
		"databases":         databaseCount,
		"api_backends":      []string{sessionA.Backends[localagent.RouteAPI].Network, sessionB.Backends[localagent.RouteAPI].Network},
		"frontend_routes":   []string{sessionA.RouteManifest.Routes["root"].URL, sessionB.RouteManifest.Routes["root"].URL},
		"diagnostics":       len(diagnostics),
	}
	if hasErrorDiagnostics(diagnostics) {
		return summary, diagnostics, fmt.Errorf("parallel worktree runtime isolation check failed")
	}
	return summary, diagnostics, nil
}

func harnessParallelConfig(frontendAddr string) app.Config {
	return app.Config{
		Name: "parallel",
		ID:   "parallel-app",
		Root: "web",
		Frontends: map[string]app.FrontendConfig{
			"web": {
				Root:                "apps/web",
				Upstream:            frontendAddr,
				AllowSharedUpstream: true,
			},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
}

func writeHarnessParallelObservability(ctx context.Context, store *devdash.Store, appID string, sessionA, sessionB *localagent.Session) error {
	now := time.Now().UTC()
	for _, session := range []*localagent.Session{sessionA, sessionB} {
		if err := store.UpsertApp(ctx, devdash.AppRecord{
			RouteID:      session.SessionID,
			ID:           appID,
			BaseAppID:    session.BaseAppID,
			RuntimeAppID: session.RuntimeAppID,
			SessionID:    session.SessionID,
			Name:         "parallel",
			Root:         session.AppRoot,
			Routes:       session.RouteManifest.URLs(),
			Running:      true,
			UpdatedAt:    now,
		}); err != nil {
			return err
		}
		if err := store.WriteProcessOutput(ctx, devdash.ProcessOutput{
			AppID:     appID,
			SessionID: session.SessionID,
			PID:       fmt.Sprintf("%d", os.Getpid()),
			Stream:    "stdout",
			Output:    []byte("hello " + session.SessionID),
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func validateHarnessParallelState(ctx context.Context, repoRoot string, server *localagent.Server, store *devdash.Store, appID string, sessionA, sessionB *localagent.Session, databaseEnvA, databaseEnvB []string, databaseA, databaseB postgresdb.Database, dockerAvailable bool) []checkDiagnostic {
	var diagnostics []checkDiagnostic
	check := func(ok bool, message string) {
		if ok {
			return
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "parallel worktree runtimes",
			Severity:        "error",
			Message:         message,
			SuggestedAction: "Fix local agent runtime isolation, then rerun `go run ./scripts/verify -o json`.",
		})
	}
	check(sessionA.SessionID != "" && sessionB.SessionID != "" && sessionA.SessionID != sessionB.SessionID, "sessions must have distinct IDs")
	check(sessionA.RuntimeAppID != sessionB.RuntimeAppID, "runtime app IDs must be distinct")
	check(sessionA.StateRoot != sessionB.StateRoot, "session state roots must be distinct")
	check(sessionA.Backends[localagent.RouteAPI].Network == "unix" && sessionB.Backends[localagent.RouteAPI].Network == "unix", "default API backends must use Unix sockets")
	check(sessionA.Backends[localagent.RouteAPI].Addr != sessionB.Backends[localagent.RouteAPI].Addr, "API backends must be distinct")
	check(sessionA.Backends["web"].Addr != sessionB.Backends["web"].Addr, "frontend backends must be distinct")
	rootA, rootAOK := sessionA.RouteManifest.Routes["root"]
	rootB, rootBOK := sessionB.RouteManifest.Routes["root"]
	check(rootAOK && rootBOK && rootA.Kind == "frontend" && rootB.Kind == "frontend" && rootA.Backend == "web" && rootB.Backend == "web", "root frontend routes must target the configured frontend")
	check(routeIsSessionScoped(sessionA, "root") && routeIsSessionScoped(sessionB, "root") && rootA.URL != rootB.URL, "frontend routes must be session-scoped")
	_, namedA := sessionA.RouteManifest.Routes["web"]
	_, namedB := sessionB.RouteManifest.Routes["web"]
	check(!namedA && !namedB, "root frontends must not retain duplicate named routes")
	if dockerAvailable {
		check(databaseA.Database != "" && databaseB.Database != "" && databaseA.Database != databaseB.Database, "managed Postgres app databases must be distinct")
		check(envValueFromList(databaseEnvA, "DATABASE_URL") != "" && envValueFromList(databaseEnvB, "DATABASE_URL") != "" && envValueFromList(databaseEnvA, "DATABASE_URL") != envValueFromList(databaseEnvB, "DATABASE_URL"), "managed Postgres database URLs must be distinct")
	}
	substrate, ok := server.GetSubstrate(localagent.SubstrateVictoria)
	check(ok && victoria.FromSubstrate(substrate).Endpoint("traces") != "", "shared Victoria substrate must expose its trace endpoint")
	logsA, errA := store.ListProcessOutputForSession(ctx, appID, sessionA.SessionID, 10)
	logsB, errB := store.ListProcessOutputForSession(ctx, appID, sessionB.SessionID, 10)
	check(errA == nil && errB == nil && len(logsA) == 1 && len(logsB) == 1 && string(logsA[0].Output) != string(logsB[0].Output), "process output must remain session-scoped")
	appA, errA := store.GetAppForSession(ctx, appID, sessionA.SessionID)
	appB, errB := store.GetAppForSession(ctx, appID, sessionB.SessionID)
	check(errA == nil && errB == nil && appA.SessionID != "" && appB.SessionID != "" && appA.SessionID != appB.SessionID, "dashboard app sessions must remain session-scoped")
	for index, session := range []*localagent.Session{sessionA, sessionB} {
		if _, err := runHarnessAppCLI(ctx, repoRoot, session.AppRoot, server.Paths().Home, "down", "-o", "json"); err != nil {
			check(false, "stopping the selected private session failed: "+err.Error())
			break
		}
		paths, err := localagent.PathsForWorktree(server.Paths().Home, session.AppRoot)
		if err != nil {
			check(false, "resolving the selected private session failed: "+err.Error())
			break
		}
		held, err := paths.ProbeLiveLock()
		check(err == nil && !held, "selected runtime retained its live lock after down")
		if index == 0 {
			other, err := harnessLiveSession(ctx, server.Paths().Home, sessionB.AppRoot)
			check(err == nil && other.OwnerPID == sessionB.OwnerPID, "stopping A changed the live B runtime")
		}
	}
	return diagnostics
}

func routeIsSessionScoped(session *localagent.Session, route string) bool {
	if session == nil {
		return false
	}
	value := strings.TrimSpace(session.RouteManifest.Routes[route].URL)
	if value == "" {
		return false
	}
	if session.RouteManifest.Mode == localagent.RouteModePath {
		baseURL := strings.TrimRight(strings.TrimSpace(session.RouteManifest.BaseURL), "/")
		if baseURL == "" || !strings.HasPrefix(value, baseURL+"/") {
			return false
		}
		return session.RouteManifest.PortLease != nil && session.RouteManifest.PortLease.SessionID == session.SessionID
	}
	return routeContainsSession(value, session.SessionID)
}

func routeContainsSession(route, sessionID string) bool {
	return strings.Contains(route, "."+sessionID+".") || strings.Contains(route, "/s/"+sessionID)
}

func envValueFromList(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func waitForHarnessAgent(ctx context.Context, client *localagent.Client) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	if lastErr != nil {
		return lastErr
	}
	return ctx.Err()
}

func reserveHarnessAddr() (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", func() {}, err
	}
	return ln.Addr().String(), func() { _ = ln.Close() }, nil
}

func patchEnv(values map[string]*string) func() {
	type oldValue struct {
		value string
		ok    bool
	}
	old := make(map[string]oldValue, len(values))
	for key, next := range values {
		value, ok := envpolicy.Lookup(key)
		old[key] = oldValue{value: value, ok: ok}
		if next == nil {
			_ = envpolicy.Unset(key)
		} else {
			_ = envpolicy.Set(key, *next)
		}
	}
	return func() {
		for key, value := range old {
			if value.ok {
				_ = envpolicy.Set(key, value.value)
			} else {
				_ = envpolicy.Unset(key)
			}
		}
	}
}

func stringPtr(value string) *string {
	return &value
}

func harnessRandomLabel() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
