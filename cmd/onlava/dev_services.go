package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lib/pq"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

const (
	devPostgresDefaultVersion   = "18"
	devPostgresDefaultIsolation = "database"
	devPostgresAdminURLEnv      = "ONLAVA_DEV_POSTGRES_ADMIN_URL"
	devPostgresBinEnv           = "ONLAVA_DEV_POSTGRES_BIN"
	devPostgresInitDBEnv        = "ONLAVA_DEV_POSTGRES_INITDB"
	devPostgresExternalEnv      = "ONLAVA_DEV_POSTGRES_EXTERNAL"
	devElectricDefaultRoute     = "electric"
	devElectricUpstreamEnv      = "ONLAVA_DEV_ELECTRIC_UPSTREAM"
	devElectricBinEnv           = "ONLAVA_DEV_ELECTRIC_BIN"
	devElectricContainerPort    = 3000
)

type managedPostgresPlan struct {
	ServiceName  string
	Version      string
	Isolation    string
	AdminURL     string
	AdminURLFrom string
	DatabaseName string
	DatabaseURL  string
}

type managedElectricPlan struct {
	ServiceName string
	Route       string
	Upstream    string
	Image       string
	Database    string
	Env         map[string]string
}

type localPostgresBinaries struct {
	InitDB   string
	Postgres string
}

type managedPostgresServer struct {
	AdminURL  string
	DataDir   string
	SocketDir string
	Port      int
	Source    string
	Version   string
	ownerPID  int
	cmd       *exec.Cmd
	done      chan error
}

type managedElectricService struct {
	Route    string
	Addr     string
	Source   string
	LogPath  string
	cmd      *exec.Cmd
	done     chan error
	external bool
}

func managedPostgresDeclared(cfg app.Config) (string, app.DevServiceConfig, bool) {
	for name, svc := range cfg.Dev.Services {
		kind := strings.TrimSpace(svc.Kind)
		if kind == "" && name == "postgres" {
			kind = "postgres"
		}
		if kind == "postgres" {
			return name, svc, true
		}
	}
	return "", app.DevServiceConfig{}, false
}

func managedElectricDeclared(cfg app.Config) (string, app.DevServiceConfig, bool) {
	for name, svc := range cfg.Dev.Services {
		kind := strings.TrimSpace(svc.Kind)
		if kind == "" && name == "electric" {
			kind = "electric"
		}
		if kind == "electric" {
			return name, svc, true
		}
	}
	return "", app.DevServiceConfig{}, false
}

func resolveManagedPostgresPlan(cfg app.Config, session *localagent.Session, env []string) (*managedPostgresPlan, error) {
	name, svc, ok := managedPostgresDeclared(cfg)
	if !ok {
		return nil, nil
	}
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil, fmt.Errorf("dev.services.%s requires an active onlava agent session", name)
	}
	isolation := firstNonEmpty(strings.TrimSpace(svc.Isolation), devPostgresDefaultIsolation)
	if isolation != devPostgresDefaultIsolation {
		return nil, fmt.Errorf("dev.services.%s isolation %q is not supported yet; use %q", name, isolation, devPostgresDefaultIsolation)
	}
	adminURL, source := lookupEnvValue(env, devPostgresAdminURLEnv)
	if adminURL == "" {
		return nil, fmt.Errorf("dev.services.%s requires %s, a reusable agent Postgres substrate, or local initdb/postgres binaries", name, devPostgresAdminURLEnv)
	}
	dbName := managedPostgresDatabaseName(firstNonEmpty(session.BaseAppID, cfg.AppID()), session.SessionID)
	dbURL, err := postgresDatabaseURL(adminURL, dbName)
	if err != nil {
		return nil, err
	}
	return &managedPostgresPlan{
		ServiceName:  name,
		Version:      firstNonEmpty(strings.TrimSpace(svc.Version), devPostgresDefaultVersion),
		Isolation:    isolation,
		AdminURL:     adminURL,
		AdminURLFrom: source,
		DatabaseName: dbName,
		DatabaseURL:  dbURL,
	}, nil
}

func managedPostgresEnv(ctx context.Context, cfg app.Config, session *localagent.Session, baseEnv []string, agent *localagent.Client) ([]string, error) {
	if _, _, ok := managedPostgresDeclared(cfg); !ok {
		return nil, nil
	}
	if managedPostgresUsesExternalDatabase(baseEnv) {
		return nil, nil
	}
	baseEnv, err := envWithManagedPostgresAdminURL(ctx, cfg, baseEnv, agent)
	if err != nil {
		return nil, err
	}
	plan, err := resolveManagedPostgresPlan(cfg, session, baseEnv)
	if err != nil || plan == nil {
		return nil, err
	}
	if err := ensureManagedPostgresDatabaseFn(ctx, plan.AdminURL, plan.DatabaseName); err != nil {
		return nil, err
	}
	if agent != nil {
		req := localagent.UpsertSubstrateRequest{
			Kind:     localagent.SubstratePostgres,
			Status:   "ready",
			OwnerPID: os.Getpid(),
			URLs: map[string]string{
				"admin":                        plan.AdminURL,
				"session." + session.SessionID: plan.DatabaseURL,
			},
			Endpoints: map[string]string{
				"version":                      plan.Version,
				"isolation":                    plan.Isolation,
				"session." + session.SessionID: plan.DatabaseName,
			},
		}
		if current, err := agent.GetSubstrate(ctx, localagent.SubstratePostgres); err == nil {
			req.PIDs = current.PIDs
			if current.OwnerPID > 0 {
				req.OwnerPID = current.OwnerPID
			}
			req.URLs = mergeManagedStrings(current.URLs, req.URLs)
			req.Endpoints = mergeManagedStrings(current.Endpoints, req.Endpoints)
		}
		_, _ = agent.UpsertSubstrate(ctx, req)
	}
	return []string{
		"DatabaseURL=" + plan.DatabaseURL,
		"DATABASE_URL=" + plan.DatabaseURL,
		"ONLAVA_MANAGED_DATABASE_URL=" + plan.DatabaseURL,
		"ONLAVA_MANAGED_DATABASE_NAME=" + plan.DatabaseName,
	}, nil
}

func resolveManagedElectricPlan(cfg app.Config, env []string) (*managedElectricPlan, error) {
	name, svc, ok := managedElectricDeclared(cfg)
	if !ok {
		return nil, nil
	}
	route := strings.TrimSpace(svc.Route)
	if route == "" {
		route = devElectricDefaultRoute
	}
	route = localagentLabel(route)
	if route == "" {
		return nil, fmt.Errorf("dev.services.%s route must not be empty", name)
	}
	upstream, _ := lookupEnvValue(env, devElectricUpstreamEnv)
	upstream = normalizeManagedTCPUpstream(upstream)
	if upstream == "" {
		return &managedElectricPlan{
			ServiceName: name,
			Route:       route,
			Image:       strings.TrimSpace(svc.Image),
			Database:    localagentLabel(svc.Database),
			Env:         copyManagedEnv(svc.Env),
		}, nil
	}
	return &managedElectricPlan{
		ServiceName: name,
		Route:       route,
		Upstream:    upstream,
		Image:       strings.TrimSpace(svc.Image),
		Database:    localagentLabel(svc.Database),
		Env:         copyManagedEnv(svc.Env),
	}, nil
}

func managedElectricBackends(cfg app.Config, env []string) (map[string]localagent.Backend, error) {
	plan, err := resolveManagedElectricPlan(cfg, env)
	if err != nil || plan == nil || plan.Upstream == "" {
		return nil, err
	}
	return map[string]localagent.Backend{
		plan.Route: {Network: "tcp", Addr: plan.Upstream},
	}, nil
}

func managedElectricEnv(cfg app.Config, session *localagent.Session, baseEnv []string) ([]string, error) {
	plan, err := resolveManagedElectricPlan(cfg, baseEnv)
	if err != nil || plan == nil || session == nil {
		return nil, err
	}
	var env []string
	if !hasEnvValue(baseEnv, "ELECTRIC_URL") {
		if routeURL := strings.TrimSpace(session.Routes[plan.Route]); routeURL != "" {
			env = append(env, "ELECTRIC_URL="+routeURL)
			env = append(env, "ONLAVA_ELECTRIC_URL="+routeURL)
		}
	}
	return env, nil
}

func (s *devSupervisor) ensureManagedElectric(ctx context.Context) error {
	if s == nil || s.electric != nil {
		return nil
	}
	if _, _, ok := managedElectricDeclared(s.cfg); !ok {
		return nil
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return err
	}
	plan, err := resolveManagedElectricPlan(s.cfg, baseEnv)
	if err != nil || plan == nil {
		return err
	}
	if plan.Upstream != "" {
		return nil
	}
	if s.agent == nil || s.agentSession == nil {
		return fmt.Errorf("dev.services.%s requires an active onlava agent session", plan.ServiceName)
	}
	service, backend, err := startManagedElectricService(ctx, s.root, s.cfg, s.agentSession, plan, baseEnv, s.agent)
	if err != nil {
		return err
	}
	s.electric = service
	backends := copyManagedBackends(s.agentSession.Backends)
	backends[plan.Route] = backend
	session, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.cfg.AppID(),
		AppRoot:     s.root,
		SessionID:   s.agentSession.SessionID,
		Branch:      s.agentSession.Branch,
		Status:      firstNonEmpty(s.agentSession.Status, "starting"),
		OwnerPID:    os.Getpid(),
		AppPID:      s.agentSession.AppPID,
		Backends:    backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		return err
	}
	s.agentSession = &session
	s.setSessionIdentity(&session)
	if s.console != nil && s.console.verbose {
		s.console.Event("electric.managed", map[string]any{
			"route":  plan.Route,
			"addr":   service.Addr,
			"source": service.Source,
		})
	}
	return nil
}

func startManagedElectricService(ctx context.Context, root string, cfg app.Config, session *localagent.Session, plan *managedElectricPlan, baseEnv []string, agent *localagent.Client) (*managedElectricService, localagent.Backend, error) {
	if plan == nil {
		return nil, localagent.Backend{}, nil
	}
	dbURL, err := managedElectricDatabaseURL(ctx, root, cfg, session, plan, baseEnv, agent)
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	logPath := filepath.Join(session.StateRoot, "run", "electric.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, localagent.Backend{}, err
	}
	streamID := managedElectricReplicationStreamID(session)
	if bin, _ := lookupEnvValue(baseEnv, devElectricBinEnv); bin != "" {
		return startManagedElectricBinary(ctx, root, plan, baseEnv, dbURL, port, addr, logPath, bin, streamID)
	}
	if bin, err := execLookPath("electric"); err == nil {
		return startManagedElectricBinary(ctx, root, plan, baseEnv, dbURL, port, addr, logPath, bin, streamID)
	}
	if plan.Image != "" {
		if docker, err := execLookPath("docker"); err == nil {
			return startManagedElectricContainer(ctx, root, plan, baseEnv, dbURL, port, addr, logPath, docker, streamID)
		}
	}
	return nil, localagent.Backend{}, fmt.Errorf("dev.services.%s needs %s, %s, or dev.services.%s.image with docker available", plan.ServiceName, devElectricUpstreamEnv, devElectricBinEnv, plan.ServiceName)
}

func managedElectricDatabaseURL(ctx context.Context, root string, cfg app.Config, session *localagent.Session, plan *managedElectricPlan, baseEnv []string, agent *localagent.Client) (string, error) {
	if plan.Database == "" || plan.Database == "postgres" {
		if _, _, ok := managedPostgresDeclared(cfg); ok && !managedPostgresUsesExternalDatabase(baseEnv) {
			env, err := envWithManagedPostgresAdminURL(ctx, cfg, baseEnv, agent)
			if err != nil {
				return "", err
			}
			pg, err := resolveManagedPostgresPlan(cfg, session, env)
			if err != nil {
				return "", err
			}
			if pg != nil {
				if err := ensureManagedPostgresDatabaseFn(ctx, pg.AdminURL, pg.DatabaseName); err != nil {
					return "", err
				}
				return pg.DatabaseURL, nil
			}
		}
	}
	for _, key := range []string{"DATABASE_URL", "DatabaseURL"} {
		if value, _ := lookupEnvValue(baseEnv, key); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("dev.services.%s needs DATABASE_URL/DatabaseURL or dev.services.postgres", plan.ServiceName)
}

func startManagedElectricBinary(ctx context.Context, root string, plan *managedElectricPlan, baseEnv []string, dbURL string, port int, addr, logPath, bin, streamID string) (*managedElectricService, localagent.Backend, error) {
	if !isExecutableFile(bin) {
		return nil, localagent.Backend{}, fmt.Errorf("%s points to a non-executable file: %s", devElectricBinEnv, bin)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	cmd := commandTreeContext(ctx, bin)
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = managedElectricProcessEnv(plan, baseEnv, dbURL, port, streamID)
	service := &managedElectricService{Route: plan.Route, Addr: addr, Source: "binary", LogPath: logPath, cmd: cmd, done: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, localagent.Backend{}, err
	}
	go func() {
		service.done <- cmd.Wait()
		close(service.done)
		_ = logFile.Close()
	}()
	if err := waitForManagedElectric(ctx, service); err != nil {
		_ = service.Interrupt()
		return nil, localagent.Backend{}, err
	}
	return service, localagent.Backend{Network: "tcp", Addr: addr}, nil
}

func startManagedElectricContainer(ctx context.Context, root string, plan *managedElectricPlan, baseEnv []string, dbURL string, port int, addr, logPath, docker, streamID string) (*managedElectricService, localagent.Backend, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	containerEnv := managedElectricContainerEnv(plan, baseEnv, dbURL, streamID)
	args := []string{"run", "--rm", "--pull", "missing", "--add-host", "host.docker.internal:host-gateway", "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, devElectricContainerPort)}
	for _, item := range containerEnv {
		args = append(args, "-e", item)
	}
	args = append(args, plan.Image)
	cmd := commandTreeContext(ctx, docker, args...)
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = baseEnv
	service := &managedElectricService{Route: plan.Route, Addr: addr, Source: "container", LogPath: logPath, cmd: cmd, done: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, localagent.Backend{}, err
	}
	go func() {
		service.done <- cmd.Wait()
		close(service.done)
		_ = logFile.Close()
	}()
	if err := waitForManagedElectric(ctx, service); err != nil {
		_ = service.Interrupt()
		return nil, localagent.Backend{}, err
	}
	return service, localagent.Backend{Network: "tcp", Addr: addr}, nil
}

func managedElectricProcessEnv(plan *managedElectricPlan, baseEnv []string, dbURL string, port int, streamID string) []string {
	portValue := fmt.Sprintf("%d", port)
	overrides := map[string]string{
		"DatabaseURL":   dbURL,
		"DATABASE_URL":  dbURL,
		"ELECTRIC_PORT": portValue,
		"PORT":          portValue,
	}
	if strings.TrimSpace(streamID) != "" {
		overrides["ELECTRIC_REPLICATION_STREAM_ID"] = streamID
	}
	env := envWithManagedOverrides(baseEnv, overrides)
	values := envListMap(env)
	for key, value := range plan.Env {
		env = envWithManagedOverrides(env, map[string]string{key: os.Expand(strings.TrimSpace(value), func(name string) string {
			return values[name]
		})})
		values = envListMap(env)
	}
	return env
}

func managedElectricContainerEnv(plan *managedElectricPlan, baseEnv []string, dbURL string, streamID string) []string {
	dbURL = databaseURLForContainer(dbURL)
	env := managedElectricProcessEnv(plan, baseEnv, dbURL, devElectricContainerPort, streamID)
	values := envListMap(env)
	keys := []string{"DATABASE_URL", "DatabaseURL", "ELECTRIC_PORT", "PORT", "ELECTRIC_REPLICATION_STREAM_ID"}
	for key := range plan.Env {
		keys = append(keys, key)
	}
	seen := map[string]bool{}
	var out []string
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if value := strings.TrimSpace(values[key]); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func databaseURLForContainer(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return raw
	}
	host := parsed.Hostname()
	switch host {
	case "127.0.0.1", "localhost", "::1":
	default:
		return raw
	}
	port := parsed.Port()
	if port == "" {
		parsed.Host = "host.docker.internal"
	} else {
		parsed.Host = net.JoinHostPort("host.docker.internal", port)
	}
	return parsed.String()
}

func managedElectricReplicationStreamID(session *localagent.Session) string {
	if session == nil {
		return ""
	}
	return managedPostgresDatabaseName("onlava", session.SessionID)
}

func waitForManagedElectric(ctx context.Context, service *managedElectricService) error {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-service.done:
			if err != nil {
				return fmt.Errorf("managed Electric exited before becoming ready: %w", err)
			}
			return fmt.Errorf("managed Electric exited before becoming ready")
		case <-timer.C:
			return fmt.Errorf("managed Electric at %s did not accept TCP connections within 30s; see %s", service.Addr, service.LogPath)
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", service.Addr, 200*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func (s *managedElectricService) Interrupt() error {
	if s == nil || s.external || s.cmd == nil {
		return nil
	}
	if err := interruptProcessTree(s.cmd); err != nil {
		return err
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		_ = killProcessTree(s.cmd)
	}
	return nil
}

func managedPostgresPlanForCurrentSession(ctx context.Context, appRoot string, cfg app.Config, env []string) (*managedPostgresPlan, error) {
	if _, _, ok := managedPostgresDeclared(cfg); !ok {
		return nil, nil
	}
	session, err := currentAgentSessionForAppRoot(ctx, appRoot)
	if err != nil {
		return nil, err
	}
	client, err := localagent.DefaultClient()
	if err == nil {
		env, err = envWithManagedPostgresAdminURL(ctx, cfg, env, client)
		if err != nil {
			return nil, err
		}
	}
	return resolveManagedPostgresPlan(cfg, session, env)
}

func envWithManagedPostgresAdminURL(ctx context.Context, cfg app.Config, env []string, agent *localagent.Client) ([]string, error) {
	if hasEnvValue(env, devPostgresAdminURLEnv) {
		return env, nil
	}
	withAgent := envWithManagedPostgresAgentAdminURL(ctx, env, agent)
	if adminURL, _ := lookupEnvValue(withAgent, devPostgresAdminURLEnv); adminURL != "" {
		if managedPostgresAdminReachable(ctx, adminURL) && postgresAdminVersionMatches(ctx, adminURL, postgresServiceVersion(cfg)) {
			return withAgent, nil
		}
		if agent != nil {
			_, _ = agent.DeleteSubstrate(ctx, localagent.SubstratePostgres)
		}
	}
	if agent == nil {
		return env, nil
	}
	adminURL, err := ensureLocalManagedPostgresSubstrate(ctx, cfg, agent)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(adminURL) == "" {
		return env, nil
	}
	return append(append([]string(nil), env...), devPostgresAdminURLEnv+"="+adminURL), nil
}

func envWithManagedPostgresAgentAdminURL(ctx context.Context, env []string, agent *localagent.Client) []string {
	if agent == nil || hasEnvValue(env, devPostgresAdminURLEnv) {
		return env
	}
	substrate, err := agent.GetSubstrate(ctx, localagent.SubstratePostgres)
	if err != nil {
		return env
	}
	if err := verifySubstrateOwner(substrate); err != nil {
		_, _ = agent.DeleteSubstrate(ctx, localagent.SubstratePostgres)
		return env
	}
	if adminURL := strings.TrimSpace(substrate.URLs["admin"]); adminURL != "" {
		return append(append([]string(nil), env...), devPostgresAdminURLEnv+"="+adminURL)
	}
	return env
}

func ensureLocalManagedPostgresSubstrate(ctx context.Context, cfg app.Config, agent *localagent.Client) (string, error) {
	if agent == nil {
		return "", nil
	}
	if substrate, err := agent.GetSubstrate(ctx, localagent.SubstratePostgres); err == nil {
		if adminURL := strings.TrimSpace(substrate.URLs["admin"]); adminURL != "" {
			if verifySubstrateOwner(substrate) == nil && managedPostgresAdminReachable(ctx, adminURL) && postgresAdminVersionMatches(ctx, adminURL, postgresServiceVersion(cfg)) {
				return adminURL, nil
			}
			_, _ = agent.DeleteSubstrate(ctx, localagent.SubstratePostgres)
		}
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return "", err
	}
	root := filepath.Join(paths.AgentDir, "postgres")
	requestedVersion := firstNonEmpty(strings.TrimSpace(postgresServiceVersion(cfg)), devPostgresDefaultVersion)
	server, err := startLocalManagedPostgres(ctx, root, requestedVersion)
	if err != nil {
		return "", err
	}
	if _, err := agent.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstratePostgres,
		Status:   "ready",
		OwnerPID: serverPID(server),
		PIDs:     map[string]int{"server": serverPID(server)},
		URLs:     map[string]string{"admin": server.AdminURL},
		Endpoints: map[string]string{
			"version":    firstNonEmpty(server.Version, requestedVersion),
			"isolation":  devPostgresDefaultIsolation,
			"data-dir":   server.DataDir,
			"socket-dir": server.SocketDir,
			"port":       fmt.Sprintf("%d", server.Port),
			"source":     firstNonEmpty(server.Source, "local-binary"),
		},
	}); err != nil {
		_ = interruptManagedPostgresServer(server)
		return "", err
	}
	return server.AdminURL, nil
}

func verifySubstrateOwner(substrate localagent.Substrate) error {
	if substrate.Owner.PID <= 0 && substrate.OwnerPID <= 0 {
		return fmt.Errorf("substrate owner is missing")
	}
	owner := substrate.Owner
	if owner.PID <= 0 {
		owner.PID = substrate.OwnerPID
	}
	return localagent.VerifyOwner(owner)
}

func postgresServiceVersion(cfg app.Config) string {
	_, svc, ok := managedPostgresDeclared(cfg)
	if !ok {
		return ""
	}
	return svc.Version
}

func startLocalManagedPostgres(ctx context.Context, root, version string) (*managedPostgresServer, error) {
	version = firstNonEmpty(strings.TrimSpace(version), devPostgresDefaultVersion)
	binaries, err := resolveLocalPostgresBinaries(os.Environ())
	var localVersion string
	if err == nil {
		if detected, versionErr := postgresBinaryMajorVersion(ctx, binaries.Postgres); versionErr == nil {
			localVersion = detected
			if localVersion == version {
				return startLocalManagedPostgresBinary(ctx, root, localVersion, binaries)
			}
		}
	}
	if docker, dockerErr := execLookPath("docker"); dockerErr == nil {
		if dockerAvailable(ctx, docker) {
			return startLocalManagedPostgresContainer(ctx, root, version, docker)
		}
	}
	if err == nil && localVersion != "" {
		return startLocalManagedPostgresBinary(ctx, root, localVersion, binaries)
	}
	if err != nil {
		return nil, err
	}
	detected, versionErr := postgresBinaryMajorVersion(ctx, binaries.Postgres)
	if versionErr != nil {
		return nil, versionErr
	}
	return nil, fmt.Errorf("managed Postgres needs version %s but local postgres is version %s and docker is unavailable", version, detected)
}

func startLocalManagedPostgresBinary(ctx context.Context, root, version string, binaries localPostgresBinaries) (*managedPostgresServer, error) {
	dataDir := filepath.Join(root, "data-"+version)
	socketDir := filepath.Join(root, "run")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := initLocalPostgresDataDir(ctx, binaries.InitDB, root, dataDir); err != nil {
			return nil, err
		}
	}
	port, err := localPostgresPort(root)
	if err != nil {
		return nil, err
	}
	adminURL := localPostgresAdminURL(socketDir, port)
	if managedPostgresAdminReachable(ctx, adminURL) {
		if owner, err := verifyManagedPostgresPortOwner(root); err == nil {
			return &managedPostgresServer{AdminURL: adminURL, DataDir: dataDir, SocketDir: socketDir, Port: port, Source: "local-binary", Version: version, ownerPID: owner.PID}, nil
		}
		port, err = resetLocalPostgresPort(root)
		if err != nil {
			return nil, err
		}
		adminURL = localPostgresAdminURL(socketDir, port)
	}
	logFile, err := os.OpenFile(filepath.Join(root, "postgres.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-D", dataDir, "-k", socketDir, "-h", "", "-p", fmt.Sprintf("%d", port)}, managedPostgresServerArgs()...)
	cmd := exec.CommandContext(context.Background(), binaries.Postgres, args...)
	configureDetachedChildProcess(cmd)
	configureCommandCancellation(cmd, 5*time.Second)
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	server := &managedPostgresServer{
		AdminURL:  adminURL,
		DataDir:   dataDir,
		SocketDir: socketDir,
		Port:      port,
		Source:    "local-binary",
		Version:   version,
		ownerPID:  cmd.Process.Pid,
		cmd:       cmd,
		done:      make(chan error, 1),
	}
	go func() {
		server.done <- cmd.Wait()
		close(server.done)
		_ = logFile.Close()
	}()
	if err := waitForManagedPostgres(ctx, server); err != nil {
		_ = interruptManagedPostgresServer(server)
		return nil, err
	}
	if err := writeManagedPostgresPortOwner(root, localagent.CaptureOwner(cmd.Process.Pid, "managed postgres")); err != nil {
		_ = interruptManagedPostgresServer(server)
		return nil, err
	}
	return server, nil
}

func startLocalManagedPostgresContainer(ctx context.Context, root, version, docker string) (*managedPostgresServer, error) {
	dataDir := filepath.Join(root, "docker-"+version)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	port, err := localPostgresPort(root)
	if err != nil {
		return nil, err
	}
	adminURL := localPostgresTCPAdminURL(port)
	if managedPostgresAdminReachable(ctx, adminURL) && postgresAdminVersionMatches(ctx, adminURL, version) {
		if owner, err := verifyManagedPostgresPortOwner(root); err == nil {
			return &managedPostgresServer{AdminURL: adminURL, DataDir: dataDir, Port: port, Source: "docker", Version: version, ownerPID: owner.PID}, nil
		}
		port, err = resetLocalPostgresPort(root)
		if err != nil {
			return nil, err
		}
		adminURL = localPostgresTCPAdminURL(port)
	}
	logFile, err := os.OpenFile(filepath.Join(root, "postgres-docker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	containerName := managedPostgresContainerName(root, version, port)
	args := []string{
		"run", "--rm", "--pull", "missing",
		"--name", containerName,
		"-e", "POSTGRES_USER=onlava",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=postgres",
		"-p", fmt.Sprintf("127.0.0.1:%d:5432", port),
		"-v", dataDir + ":/var/lib/postgresql",
		"postgres:" + version,
	}
	args = append(args, managedPostgresServerArgs()...)
	cmd := commandTreeContext(context.Background(), docker, args...)
	configureDetachedChildProcess(cmd)
	configureCommandCancellation(cmd, 5*time.Second)
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	server := &managedPostgresServer{
		AdminURL: adminURL,
		DataDir:  dataDir,
		Port:     port,
		Source:   "docker",
		Version:  version,
		ownerPID: cmd.Process.Pid,
		cmd:      cmd,
		done:     make(chan error, 1),
	}
	go func() {
		server.done <- cmd.Wait()
		close(server.done)
		_ = logFile.Close()
	}()
	if err := waitForManagedPostgres(ctx, server); err != nil {
		_ = interruptManagedPostgresServer(server)
		return nil, err
	}
	if err := writeManagedPostgresPortOwner(root, localagent.CaptureOwner(cmd.Process.Pid, "managed postgres docker")); err != nil {
		_ = interruptManagedPostgresServer(server)
		return nil, err
	}
	return server, nil
}

func localPostgresPort(root string) (int, error) {
	path := filepath.Join(root, "port")
	if data, err := os.ReadFile(path); err == nil {
		port, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && port > 0 {
			return port, nil
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", port)), 0o644); err != nil {
		return 0, err
	}
	return port, nil
}

func resetLocalPostgresPort(root string) (int, error) {
	port, err := freeLoopbackPort()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(root, "port"), []byte(fmt.Sprintf("%d\n", port)), 0o644); err != nil {
		return 0, err
	}
	_ = os.Remove(managedPostgresPortOwnerPath(root))
	return port, nil
}

func managedPostgresPortOwnerPath(root string) string {
	return filepath.Join(root, "owner.json")
}

func writeManagedPostgresPortOwner(root string, owner localagent.Owner) error {
	if owner.PID <= 0 {
		return fmt.Errorf("managed Postgres owner pid is missing")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(managedPostgresPortOwnerPath(root), data, 0o644)
}

func verifyManagedPostgresPortOwner(root string) (localagent.Owner, error) {
	data, err := os.ReadFile(managedPostgresPortOwnerPath(root))
	if err != nil {
		return localagent.Owner{}, err
	}
	var owner localagent.Owner
	if err := json.Unmarshal(data, &owner); err != nil {
		return localagent.Owner{}, err
	}
	if err := localagent.VerifyOwner(owner); err != nil {
		return localagent.Owner{}, err
	}
	return owner, nil
}

func managedPostgresServerArgs() []string {
	return []string{
		"-c", "wal_level=logical",
		"-c", "max_wal_senders=10",
		"-c", "max_replication_slots=10",
	}
}

func resolveLocalPostgresBinaries(env []string) (localPostgresBinaries, error) {
	initdb, _ := lookupEnvValue(env, devPostgresInitDBEnv)
	postgres, _ := lookupEnvValue(env, devPostgresBinEnv)
	if initdb == "" {
		if path, err := execLookPath("initdb"); err == nil {
			initdb = path
		}
	}
	if postgres == "" {
		if path, err := execLookPath("postgres"); err == nil {
			postgres = path
		}
	}
	if initdb != "" && postgres == "" {
		if sibling := filepath.Join(filepath.Dir(initdb), "postgres"); isExecutableFile(sibling) {
			postgres = sibling
		}
	}
	if postgres != "" && initdb == "" {
		if sibling := filepath.Join(filepath.Dir(postgres), "initdb"); isExecutableFile(sibling) {
			initdb = sibling
		}
	}
	if initdb == "" || postgres == "" {
		return localPostgresBinaries{}, fmt.Errorf("managed Postgres needs %s/%s or initdb/postgres in PATH", devPostgresInitDBEnv, devPostgresBinEnv)
	}
	if !isExecutableFile(initdb) {
		return localPostgresBinaries{}, fmt.Errorf("%s points to a non-executable file: %s", devPostgresInitDBEnv, initdb)
	}
	if !isExecutableFile(postgres) {
		return localPostgresBinaries{}, fmt.Errorf("%s points to a non-executable file: %s", devPostgresBinEnv, postgres)
	}
	return localPostgresBinaries{InitDB: initdb, Postgres: postgres}, nil
}

func initLocalPostgresDataDir(ctx context.Context, initdb, root, dataDir string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(initCtx, initdb, "-D", dataDir, "-U", "onlava", "-A", "trust")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return err
		}
		return fmt.Errorf("initdb failed: %w\n%s", err, msg)
	}
	return nil
}

func waitForManagedPostgres(ctx context.Context, server *managedPostgresServer) error {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-server.done:
			if err != nil {
				return fmt.Errorf("managed Postgres exited before becoming ready: %w", err)
			}
			return fmt.Errorf("managed Postgres exited before becoming ready")
		case <-timer.C:
			if lastErr != nil {
				return fmt.Errorf("managed Postgres did not become ready: %w", lastErr)
			}
			return fmt.Errorf("managed Postgres did not become ready")
		case <-ticker.C:
			if err := pingPostgresAdmin(ctx, server.AdminURL, 500*time.Millisecond); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
	}
}

func managedPostgresAdminReachable(ctx context.Context, adminURL string) bool {
	return pingPostgresAdmin(ctx, adminURL, 500*time.Millisecond) == nil
}

func postgresAdminVersionMatches(ctx context.Context, adminURL, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return true
	}
	got, err := postgresAdminMajorVersion(ctx, adminURL)
	return err == nil && got == want
}

func postgresAdminMajorVersion(ctx context.Context, adminURL string) (string, error) {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return "", err
	}
	defer db.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var versionNum string
	if err := db.QueryRowContext(queryCtx, `SHOW server_version_num`).Scan(&versionNum); err != nil {
		return "", err
	}
	if len(versionNum) < 2 {
		return "", fmt.Errorf("unexpected Postgres server_version_num %q", versionNum)
	}
	return strings.TrimLeft(versionNum[:len(versionNum)-4], "0"), nil
}

func pingPostgresAdmin(ctx context.Context, adminURL string, timeout time.Duration) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return db.PingContext(pingCtx)
}

func interruptManagedPostgresServer(server *managedPostgresServer) error {
	if server == nil || server.cmd == nil {
		return nil
	}
	if err := interruptProcessTree(server.cmd); err != nil {
		return err
	}
	select {
	case <-server.done:
	case <-time.After(5 * time.Second):
		_ = killProcessTree(server.cmd)
	}
	return nil
}

func serverPID(server *managedPostgresServer) int {
	if server == nil {
		return 0
	}
	if server.ownerPID > 0 {
		return server.ownerPID
	}
	if server.cmd == nil || server.cmd.Process == nil {
		return 0
	}
	return server.cmd.Process.Pid
}

func localPostgresAdminURL(socketDir string, port int) string {
	values := url.Values{}
	values.Set("host", socketDir)
	values.Set("port", fmt.Sprintf("%d", port))
	values.Set("sslmode", "disable")
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.User("onlava"),
		Path:     "/postgres",
		RawQuery: values.Encode(),
	}).String()
}

func localPostgresTCPAdminURL(port int) string {
	values := url.Values{}
	values.Set("sslmode", "disable")
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("onlava", "postgres"),
		Host:     net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)),
		Path:     "/postgres",
		RawQuery: values.Encode(),
	}).String()
}

func postgresBinaryMajorVersion(ctx context.Context, postgres string) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, postgres, "--version").Output()
	if err != nil {
		return "", err
	}
	return postgresMajorVersionFromOutput(string(output))
}

func dockerAvailable(ctx context.Context, docker string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return exec.CommandContext(checkCtx, docker, "info").Run() == nil
}

func postgresMajorVersionFromOutput(output string) (string, error) {
	fields := strings.Fields(output)
	for i := len(fields) - 1; i >= 0; i-- {
		part := strings.Trim(fields[i], " ,")
		if part == "" {
			continue
		}
		major := part
		if before, _, ok := strings.Cut(part, "."); ok {
			major = before
		}
		if _, err := strconv.Atoi(major); err == nil {
			return major, nil
		}
	}
	return "", fmt.Errorf("could not parse Postgres version from %q", strings.TrimSpace(output))
}

func managedPostgresContainerName(root, version string, port int) string {
	sum := sha256.Sum256([]byte(root + ":" + version + ":" + fmt.Sprintf("%d", port)))
	return "onlava-postgres-" + hex.EncodeToString(sum[:])[:12]
}

func currentAgentSessionForAppRoot(ctx context.Context, appRoot string) (*localagent.Session, error) {
	client, err := localagent.DefaultClient()
	if err != nil {
		return nil, err
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no onlava agent session found for %s", appRoot)
	}
	return &sessions[0], nil
}

func localagentLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeManagedTCPUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return value
}

func copyManagedEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		copied[key] = strings.TrimSpace(value)
	}
	return copied
}

func mergeManagedStrings(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	for key, value := range override {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	return merged
}

func copyManagedBackends(backends map[string]localagent.Backend) map[string]localagent.Backend {
	copied := make(map[string]localagent.Backend, len(backends)+1)
	for key, backend := range backends {
		copied[key] = backend
	}
	return copied
}

func envWithManagedOverrides(base []string, overrides map[string]string) []string {
	env := append([]string(nil), base...)
	index := make(map[string]int, len(env))
	for i, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			index[key] = i
		}
	}
	for key, value := range overrides {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		item := key + "=" + strings.TrimSpace(value)
		if i, ok := index[key]; ok {
			env[i] = item
			continue
		}
		index[key] = len(env)
		env = append(env, item)
	}
	return env
}

func ensureManagedPostgresDatabase(ctx context.Context, adminURL, dbName string) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("managed Postgres admin connection failed: %w", err)
	}
	return createPostgresDatabaseIfMissing(ctx, db, dbName)
}

func resetManagedPostgresDatabase(ctx context.Context, adminURL, dbName string) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("managed Postgres admin connection failed: %w", err)
	}
	quoted := pq.QuoteIdentifier(dbName)
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoted); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+quoted); err != nil {
		return err
	}
	return nil
}

func dropManagedPostgresDatabase(ctx context.Context, adminURL, dbName string) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("managed Postgres admin connection failed: %w", err)
	}
	quoted := pq.QuoteIdentifier(dbName)
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoted)
	return err
}

func createPostgresDatabaseIfMissing(ctx context.Context, db *sql.DB, dbName string) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := db.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(dbName))
	return err
}

func postgresDatabaseURL(rawURL, dbName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "postgres", "postgresql":
	default:
		return "", fmt.Errorf("managed Postgres URL must use postgres:// or postgresql://, got %q", parsed.Scheme)
	}
	if parsed.Host == "" && strings.TrimSpace(parsed.Query().Get("host")) == "" {
		return "", fmt.Errorf("managed Postgres URL must include a host")
	}
	parsed.Path = "/" + dbName
	return parsed.String(), nil
}

func managedPostgresDatabaseName(baseAppID, sessionID string) string {
	label := postgresIdentifierPart(baseAppID) + "_" + postgresIdentifierPart(sessionID)
	label = strings.Trim(label, "_")
	if label == "" {
		label = "onlava_session"
	}
	if len(label) <= 63 {
		return label
	}
	sum := sha256.Sum256([]byte(label))
	suffix := "_" + hex.EncodeToString(sum[:])[:8]
	return strings.TrimRight(label[:63-len(suffix)], "_") + suffix
}

func postgresIdentifierPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	underscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if !underscore && b.Len() > 0 {
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func lookupEnvValue(env []string, key string) (string, string) {
	values := envListMap(env)
	if value := strings.TrimSpace(values[key]); value != "" {
		return value, key
	}
	return "", ""
}

func hasEnvValue(env []string, key string) bool {
	value, _ := lookupEnvValue(env, key)
	return value != ""
}

func managedPostgresUsesExternalDatabase(env []string) bool {
	value, _ := lookupEnvValue(env, devPostgresExternalEnv)
	return value != "" && !isFalseEnv(value)
}

var ensureManagedPostgresDatabaseFn = ensureManagedPostgresDatabase
