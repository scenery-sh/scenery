package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	sceneryruntime "scenery.sh/runtime"
)

var runHarnessParallelDevCheckFunc = runHarnessParallelDevCheck

func runHarnessParallelDevStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "parallel worktree runtimes",
		Command: []string{"scenery", "harness", "self", "internal:parallel-dev", repoRoot},
	}
	summary, diagnostics, err := runHarnessParallelDevCheckFunc(ctx)
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
				SuggestedAction: "Fix local agent session isolation, then rerun `scenery harness self --json`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessParallelDevCheck(parent context.Context) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	agentHome := filepath.Join(os.TempDir(), "scenery-harness-parallel-"+harnessRandomLabel())
	defer os.RemoveAll(agentHome)
	restoreEnv := patchEnv(map[string]*string{
		"SCENERY_AGENT_HOME":            stringPtr(agentHome),
		"SCENERY_DEV_CACHE_DIR":         nil,
		"SCENERY_DEV_DASHBOARD_ADDR":    nil,
		"SCENERY_AGENT_DISABLE":         nil,
		"SCENERY_LOCAL_PROXY":           stringPtr("0"),
		"SCENERY_DEV_VICTORIA":          stringPtr("0"),
		"SCENERY_DEV_VICTORIA_DOWNLOAD": stringPtr("0"),
	})
	defer restoreEnv()

	server, err := localagent.NewServer(localagent.RunOptions{
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:9",
		},
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

	frontendA, closeFrontendA, err := reserveHarnessAddr()
	if err != nil {
		return nil, nil, err
	}
	defer closeFrontendA()
	frontendB, closeFrontendB, err := reserveHarnessAddr()
	if err != nil {
		return nil, nil, err
	}
	defer closeFrontendB()
	grafanaAddr, closeGrafana, err := reserveHarnessAddr()
	if err != nil {
		return nil, nil, err
	}
	defer closeGrafana()
	temporalAddr, closeTemporal, err := reserveHarnessAddr()
	if err != nil {
		return nil, nil, err
	}
	defer closeTemporal()

	root := filepath.Join(os.TempDir(), "scenery-harness-apps-"+harnessRandomLabel())
	rootA := filepath.Join(root, "worktree-a")
	rootB := filepath.Join(root, "worktree-b")
	defer os.RemoveAll(root)
	cfgA := harnessParallelConfig(frontendA)
	cfgB := harnessParallelConfig(frontendB)

	sessionA, restoreA, err := prepareHarnessParallelSession(ctx, rootA, cfgA)
	if err != nil {
		return nil, nil, err
	}
	defer restoreA()
	sessionB, restoreB, err := prepareHarnessParallelSession(ctx, rootB, cfgB)
	if err != nil {
		return nil, nil, err
	}
	defer restoreB()

	ensuredDBs := map[string]bool{}
	prevEnsure := ensureManagedPostgresDatabaseFn
	ensureManagedPostgresDatabaseFn = func(_ context.Context, _ string, dbName string) error {
		ensuredDBs[dbName] = true
		return nil
	}
	defer func() { ensureManagedPostgresDatabaseFn = prevEnsure }()
	baseEnv := []string{devPostgresAdminURLEnv + "=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"}
	pgEnvA, err := managedPostgresEnv(ctx, cfgA, sessionA, baseEnv, client)
	if err != nil {
		return nil, nil, err
	}
	pgEnvB, err := managedPostgresEnv(ctx, cfgB, sessionB, baseEnv, client)
	if err != nil {
		return nil, nil, err
	}

	supervisorA := &devSupervisor{root: rootA, cfg: cfgA, agent: client, agentSession: sessionA}
	supervisorB := &devSupervisor{root: rootB, cfg: cfgB, agent: client, agentSession: sessionB}
	supervisorA.registerAgentSessionBackend(ctx, localagent.RouteGrafana, localagent.Backend{Network: "tcp", Addr: grafanaAddr})
	supervisorB.registerAgentSessionBackend(ctx, localagent.RouteGrafana, localagent.Backend{Network: "tcp", Addr: grafanaAddr})
	supervisorA.registerAgentSessionBackend(ctx, localagent.RouteTemporal, localagent.Backend{Network: "tcp", Addr: temporalAddr})
	supervisorB.registerAgentSessionBackend(ctx, localagent.RouteTemporal, localagent.Backend{Network: "tcp", Addr: temporalAddr})
	sessionA = supervisorA.agentSession
	sessionB = supervisorB.agentSession

	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateVictoria,
		Status:   "ready",
		OwnerPID: os.Getpid(),
		URLs: map[string]string{
			"metrics": "http://" + grafanaAddr,
			"logs":    "http://" + grafanaAddr,
			"traces":  "http://" + grafanaAddr,
		},
		Endpoints: map[string]string{
			"metrics": "http://" + grafanaAddr + "/opentelemetry/v1/metrics",
			"logs":    "http://" + grafanaAddr + "/insert/opentelemetry/v1/logs",
			"traces":  "http://" + grafanaAddr + "/insert/opentelemetry/v1/traces",
		},
	}); err != nil {
		return nil, nil, err
	}

	store, err := devdash.OpenStore(filepath.Join(server.Paths().AgentDir, "dashboard"))
	if err != nil {
		return nil, nil, err
	}
	defer store.Close()
	if err := writeHarnessParallelObservability(ctx, store, cfgA.AppID(), sessionA, sessionB); err != nil {
		return nil, nil, err
	}

	diagnostics := validateHarnessParallelState(ctx, server, client, store, cfgA.AppID(), sessionA, sessionB, pgEnvA, pgEnvB, ensuredDBs, supervisorA.sessionTemporalEnv(), supervisorB.sessionTemporalEnv())
	summary := map[string]any{
		"sessions":        2,
		"databases":       len(ensuredDBs),
		"api_backends":    []string{sessionA.Backends[localagent.RouteAPI].Network, sessionB.Backends[localagent.RouteAPI].Network},
		"frontend_routes": []string{sessionA.Routes["web"], sessionB.Routes["web"]},
		"temporal_queues": []string{envValueFromList(supervisorA.sessionTemporalEnv(), sceneryruntime.DefaultTemporalTaskQueueEnv), envValueFromList(supervisorB.sessionTemporalEnv(), sceneryruntime.DefaultTemporalTaskQueueEnv)},
		"diagnostics":     len(diagnostics),
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
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {
					Host:                "web.parallel.localhost",
					Root:                "apps/web",
					Upstream:            frontendAddr,
					AllowSharedUpstream: true,
				},
			},
		},
		Dev: app.DevConfig{
			Services: map[string]app.DevServiceConfig{
				"postgres": {Kind: "postgres"},
			},
		},
		Temporal: app.TemporalConfig{Enabled: true},
	}
}

func prepareHarnessParallelSession(ctx context.Context, root string, cfg app.Config) (*localagent.Session, func(), error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, func() {}, err
	}
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"parallel","id":"parallel-app"}`), 0o644); err != nil {
		return nil, func() {}, err
	}
	client, session, _, restore, err := prepareDevAgentSession(ctx, root, cfg, devListenRequest{}, nil)
	if err != nil {
		restore()
		return nil, func() {}, err
	}
	if client == nil || session == nil {
		restore()
		return nil, func() {}, fmt.Errorf("agent session was not registered")
	}
	return session, restore, nil
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
			Routes:       session.Routes,
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

func validateHarnessParallelState(ctx context.Context, server *localagent.Server, client *localagent.Client, store *devdash.Store, appID string, sessionA, sessionB *localagent.Session, pgEnvA, pgEnvB []string, ensuredDBs map[string]bool, temporalEnvA, temporalEnvB []string) []checkDiagnostic {
	var diagnostics []checkDiagnostic
	check := func(ok bool, message string) {
		if ok {
			return
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "parallel worktree runtimes",
			Severity:        "error",
			Message:         message,
			SuggestedAction: "Fix local agent runtime isolation, then rerun `scenery harness self --json`.",
		})
	}
	check(sessionA.SessionID != "" && sessionB.SessionID != "" && sessionA.SessionID != sessionB.SessionID, "sessions must have distinct IDs")
	check(sessionA.RuntimeAppID != sessionB.RuntimeAppID, "runtime app IDs must be distinct")
	check(sessionA.StateRoot != sessionB.StateRoot, "session state roots must be distinct")
	check(sessionA.Backends[localagent.RouteAPI].Network == "unix" && sessionB.Backends[localagent.RouteAPI].Network == "unix", "default API backends must use Unix sockets")
	check(sessionA.Backends[localagent.RouteAPI].Addr != sessionB.Backends[localagent.RouteAPI].Addr, "API backends must be distinct")
	check(sessionA.Backends["web"].Addr != sessionB.Backends["web"].Addr, "frontend backends must be distinct")
	check(routeContainsSession(sessionA.Routes["web"], sessionA.SessionID) && routeContainsSession(sessionB.Routes["web"], sessionB.SessionID) && sessionA.Routes["web"] != sessionB.Routes["web"], "frontend routes must be session-scoped")
	check(routeContainsSession(sessionA.Routes[localagent.RouteGrafana], sessionA.SessionID) && routeContainsSession(sessionB.Routes[localagent.RouteGrafana], sessionB.SessionID), "Grafana routes must be session-scoped")
	check(routeContainsSession(sessionA.Routes[localagent.RouteTemporal], sessionA.SessionID) && routeContainsSession(sessionB.Routes[localagent.RouteTemporal], sessionB.SessionID), "Temporal routes must be session-scoped")
	check(envValueFromList(pgEnvA, "SCENERY_MANAGED_DATABASE_NAME") != "" && envValueFromList(pgEnvB, "SCENERY_MANAGED_DATABASE_NAME") != "" && envValueFromList(pgEnvA, "SCENERY_MANAGED_DATABASE_NAME") != envValueFromList(pgEnvB, "SCENERY_MANAGED_DATABASE_NAME"), "managed Postgres database names must be distinct")
	check(len(ensuredDBs) == 2, "managed Postgres must ensure two session databases")
	check(envValueFromList(temporalEnvA, sceneryruntime.DefaultTemporalTaskQueueEnv) != "" && envValueFromList(temporalEnvA, sceneryruntime.DefaultTemporalTaskQueueEnv) != envValueFromList(temporalEnvB, sceneryruntime.DefaultTemporalTaskQueueEnv), "Temporal task queue prefixes must be distinct")
	if victoria := (&agentDashboardController{store: store, agent: server}).dashboardVictoria(); victoria == nil || victoria.Endpoint("traces") == "" {
		check(false, "agent dashboard must read shared Victoria substrate")
	}
	logsA, errA := store.ListProcessOutputForSession(ctx, appID, sessionA.SessionID, 10)
	logsB, errB := store.ListProcessOutputForSession(ctx, appID, sessionB.SessionID, 10)
	check(errA == nil && errB == nil && len(logsA) == 1 && len(logsB) == 1 && string(logsA[0].Output) != string(logsB[0].Output), "process output must remain session-scoped")
	appA, errA := store.GetAppForSession(ctx, appID, sessionA.SessionID)
	appB, errB := store.GetAppForSession(ctx, appID, sessionB.SessionID)
	check(errA == nil && errB == nil && appA.SessionID != "" && appB.SessionID != "" && appA.SessionID != appB.SessionID, "dashboard app sessions must remain session-scoped")
	if _, err := client.Delete(ctx, sessionA.SessionID, false); err != nil {
		check(false, "deleting one session must succeed without signaling")
	} else if _, err := client.Delete(ctx, sessionB.SessionID, false); err != nil {
		check(false, "sibling session must remain after deleting the first session")
	}
	return diagnostics
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
