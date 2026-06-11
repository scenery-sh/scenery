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

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/toolchain"
)

const (
	devPostgresDefaultVersion   = "18"
	devPostgresDefaultIsolation = "database"
	devPostgresAdminURLEnv      = "SCENERY_DEV_POSTGRES_ADMIN_URL"
	devPostgresBinEnv           = "SCENERY_DEV_POSTGRES_BIN"
	devPostgresInitDBEnv        = "SCENERY_DEV_POSTGRES_INITDB"
	devPostgresExternalEnv      = "SCENERY_DEV_POSTGRES_EXTERNAL"
	devElectricDefaultRoute     = "electric"
	devElectricUpstreamEnv      = "SCENERY_DEV_ELECTRIC_UPSTREAM"
	devElectricBinEnv           = "SCENERY_DEV_ELECTRIC_BIN"
	devElectricContainerPort    = 3000
	appDatabaseURLEnv           = "DatabaseURL"
	legacyDatabaseURLEnv        = "DATABASE_URL"
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
	LogPath   string
	ownerPID  int
	cmd       *exec.Cmd
	done      chan error
}

type managedElectricService struct {
	Route    string
	Addr     string
	Source   string
	LogPath  string
	process  *devManagedProcess
	cmd      *exec.Cmd
	done     chan error
	external bool
}

type electricPostgresLock struct {
	PID           int
	Kind          string
	State         string
	WaitEventType string
	WaitEvent     string
	Query         string
	Application   string
	ClientAddr    string
	SlotName      string
}

type managedElectricStreamProcess struct {
	PID          int
	State        string
	Command      string
	Stream       string
	AppRoot      string
	SessionID    string
	RuntimeAppID string
}

var listManagedElectricStreamProcesses = scanManagedElectricStreamProcesses
var managedPostgresAdminReachableFn = managedPostgresAdminReachable
var postgresAdminVersionMatchesFn = postgresAdminVersionMatches

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
		return nil, fmt.Errorf("dev.services.%s requires an active agent-backed scenery dev runtime", name)
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
		if _, err := externalPostgresDatabaseURL(baseEnv); err != nil {
			return nil, err
		}
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
	return []string{
		appDatabaseURLEnv + "=" + plan.DatabaseURL,
		"SCENERY_MANAGED_DATABASE_URL=" + plan.DatabaseURL,
		"SCENERY_MANAGED_DATABASE_NAME=" + plan.DatabaseName,
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
			env = append(env, "SCENERY_ELECTRIC_URL="+routeURL)
		}
	}
	return env, nil
}

func (s *devSupervisor) ensureManagedElectric(ctx context.Context) error {
	if s == nil || s.currentElectric() != nil {
		return nil
	}
	if _, _, ok := managedElectricDeclared(s.cfg); !ok {
		return nil
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
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
	agentSession := s.currentAgentSession()
	if s.agent == nil || agentSession == nil {
		return fmt.Errorf("dev.services.%s requires an active agent-backed scenery dev runtime", plan.ServiceName)
	}
	service, backend, err := startManagedElectricService(ctx, s.root, s.cfg, agentSession, plan, baseEnv, s.agent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.electric = service
	s.mu.Unlock()
	backends := copyManagedBackends(agentSession.Backends)
	backends[plan.Route] = backend
	session, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.activeAppID(),
		AppRoot:     s.root,
		SessionID:   agentSession.SessionID,
		Branch:      agentSession.Branch,
		Status:      firstNonEmpty(agentSession.Status, "starting"),
		OwnerPID:    os.Getpid(),
		AppPID:      agentSession.AppPID,
		Processes:   s.sessionProcessesFor(agentSession, agentSession.AppPID),
		Backends:    backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		return err
	}
	s.storeAgentSession(&session)
	if s.console != nil && s.console.verbose {
		s.console.Event("electric.managed", map[string]any{
			"route":  plan.Route,
			"addr":   service.Addr,
			"source": service.Source,
		})
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "electric", Kind: "substrate", Name: "electric", Role: "sync-service", Status: "running", URL: "http://" + service.Addr}, "info", "managed Electric ready", map[string]any{
		"route":  plan.Route,
		"addr":   service.Addr,
		"source": service.Source,
	})
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
	streamID := managedElectricReplicationStreamID(session)
	if err := cleanupManagedElectricStreamProcesses(ctx, root, session, streamID); err != nil {
		return nil, localagent.Backend{}, err
	}
	postgresApplication := managedElectricPostgresApplicationName(root, session, streamID)
	if postgresApplication != "" {
		dbURL = postgresURLWithApplicationName(dbURL, postgresApplication)
	}
	if managedElectricUsesManagedPostgres(cfg, baseEnv) {
		if err := cleanupManagedElectricPostgresSlot(ctx, dbURL, root, session, streamID); err != nil {
			return nil, localagent.Backend{}, err
		}
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
	if bin, _ := lookupEnvValue(baseEnv, devElectricBinEnv); bin != "" {
		return startManagedElectricBinary(ctx, root, session, plan, baseEnv, dbURL, port, addr, logPath, bin, streamID)
	}
	if plan.Image != "" {
		if docker, err := execLookPath("docker"); err == nil {
			return startManagedElectricContainer(ctx, root, session, plan, baseEnv, dbURL, port, addr, logPath, docker, streamID)
		}
	}
	return nil, localagent.Backend{}, fmt.Errorf("dev.services.%s needs %s, %s, or dev.services.%s.image with docker available", plan.ServiceName, devElectricUpstreamEnv, devElectricBinEnv, plan.ServiceName)
}

func managedElectricUsesManagedPostgres(cfg app.Config, baseEnv []string) bool {
	_, _, ok := managedPostgresDeclared(cfg)
	return ok && !managedPostgresUsesExternalDatabase(baseEnv)
}

func managedElectricDatabaseURL(ctx context.Context, root string, cfg app.Config, session *localagent.Session, plan *managedElectricPlan, baseEnv []string, agent *localagent.Client) (string, error) {
	if plan.Database == "" || plan.Database == "postgres" {
		if _, _, ok := managedPostgresDeclared(cfg); ok && managedPostgresUsesExternalDatabase(baseEnv) {
			return externalPostgresDatabaseURL(baseEnv)
		}
		if _, svc, ok := managedPostgresDeclared(cfg); ok {
			if postgresServiceUsesBranching(svc) {
				dbURL, err := resolveDBBranchDatabaseURL(ctx, root, cfg, session)
				if err != nil {
					return "", fmt.Errorf("dev.services.%s could not resolve database URL from managed dev.services.postgres: %w", plan.ServiceName, err)
				}
				return dbURL, nil
			}
			env, err := envWithManagedPostgresAdminURL(ctx, cfg, baseEnv, agent)
			if err != nil {
				return "", fmt.Errorf("dev.services.%s could not resolve database URL from managed dev.services.postgres: %w", plan.ServiceName, err)
			}
			pg, err := resolveManagedPostgresPlan(cfg, session, env)
			if err != nil {
				return "", fmt.Errorf("dev.services.%s could not resolve database URL from managed dev.services.postgres: %w", plan.ServiceName, err)
			}
			if pg != nil {
				if err := ensureManagedPostgresDatabaseFn(ctx, pg.AdminURL, pg.DatabaseName); err != nil {
					return "", fmt.Errorf("dev.services.%s could not prepare managed dev.services.postgres database: %w", plan.ServiceName, err)
				}
				return pg.DatabaseURL, nil
			}
		}
	}
	for _, key := range []string{appDatabaseURLEnv, legacyDatabaseURLEnv} {
		if value, _ := lookupEnvValue(baseEnv, key); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("dev.services.%s needs managed dev.services.postgres or explicit external %s through %s=1", plan.ServiceName, appDatabaseURLEnv, devPostgresExternalEnv)
}

func startManagedElectricBinary(ctx context.Context, root string, session *localagent.Session, plan *managedElectricPlan, baseEnv []string, dbURL string, port int, addr, logPath, bin, streamID string) (*managedElectricService, localagent.Backend, error) {
	if !isExecutableFile(bin) {
		return nil, localagent.Backend{}, fmt.Errorf("%s points to a non-executable file: %s", devElectricBinEnv, bin)
	}
	env := managedElectricProcessEnv(plan, baseEnv, dbURL, port, streamID, managedElectricSessionEnv(root, session)...)
	service, err := startManagedElectricProcess(ctx, root, plan.Route, addr, "binary", logPath, bin, nil, env)
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	if err := waitForManagedElectric(ctx, service); err != nil {
		_ = service.Interrupt()
		return nil, localagent.Backend{}, err
	}
	return service, localagent.Backend{Network: "tcp", Addr: addr}, nil
}

func startManagedElectricContainer(ctx context.Context, root string, session *localagent.Session, plan *managedElectricPlan, baseEnv []string, dbURL string, port int, addr, logPath, docker, streamID string) (*managedElectricService, localagent.Backend, error) {
	containerEnv := managedElectricContainerEnv(plan, baseEnv, dbURL, streamID, managedElectricSessionEnv(root, session)...)
	args := []string{"run", "--rm", "--pull", "missing", "--add-host", "host.docker.internal:host-gateway", "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, devElectricContainerPort)}
	for _, item := range containerEnv {
		args = append(args, "-e", item)
	}
	args = append(args, plan.Image)
	service, err := startManagedElectricProcess(ctx, root, plan.Route, addr, "container", logPath, docker, args, baseEnv)
	if err != nil {
		return nil, localagent.Backend{}, err
	}
	if err := waitForManagedElectric(ctx, service); err != nil {
		_ = service.Interrupt()
		return nil, localagent.Backend{}, err
	}
	return service, localagent.Backend{Network: "tcp", Addr: addr}, nil
}

func startManagedElectricProcess(ctx context.Context, root, route, addr, source, logPath, command string, args, env []string) (*managedElectricService, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	process, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name:    "managed Electric",
		Kind:    "substrate",
		Role:    "sync-service",
		Dir:     root,
		Command: command,
		Args:    args,
		Env:     env,
		Stdout:  logFile,
		Stderr:  logFile,
	})
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	service := &managedElectricService{
		Route:   route,
		Addr:    addr,
		Source:  source,
		LogPath: logPath,
		process: process,
		cmd:     process.Cmd,
		done:    make(chan error, 1),
	}
	go func() {
		<-process.done
		process.mu.Lock()
		waitErr := process.waitErr
		process.mu.Unlock()
		service.done <- waitErr
		close(service.done)
		<-process.outputDone
		_ = logFile.Close()
	}()
	return service, nil
}

func managedElectricProcessEnv(plan *managedElectricPlan, baseEnv []string, dbURL string, port int, streamID string, extra ...string) []string {
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
	for _, item := range extra {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.TrimSpace(key) != "" {
			overrides[strings.TrimSpace(key)] = value
		}
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

func managedElectricContainerEnv(plan *managedElectricPlan, baseEnv []string, dbURL string, streamID string, extra ...string) []string {
	dbURL = databaseURLForContainer(dbURL)
	env := managedElectricProcessEnv(plan, baseEnv, dbURL, devElectricContainerPort, streamID, extra...)
	values := envListMap(env)
	keys := []string{"DATABASE_URL", "DatabaseURL", "ELECTRIC_PORT", "PORT", "ELECTRIC_REPLICATION_STREAM_ID"}
	for _, item := range extra {
		if key, _, ok := strings.Cut(item, "="); ok {
			keys = append(keys, key)
		}
	}
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

func managedElectricSessionEnv(root string, session *localagent.Session) []string {
	if session == nil {
		return nil
	}
	baseAppID := strings.TrimSpace(session.BaseAppID)
	runtimeAppID := strings.TrimSpace(session.RuntimeAppID)
	if runtimeAppID == "" && baseAppID != "" && strings.TrimSpace(session.SessionID) != "" {
		runtimeAppID = baseAppID + "--" + strings.TrimSpace(session.SessionID)
	}
	return []string{
		"SCENERY_APP_ROOT=" + root,
		"SCENERY_SESSION_ID=" + strings.TrimSpace(session.SessionID),
		"SCENERY_BASE_APP_ID=" + baseAppID,
		"SCENERY_RUNTIME_APP_ID=" + runtimeAppID,
		"SCENERY_DEV_SUPERVISOR=1",
		fmt.Sprintf("SCENERY_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"SCENERY_ROLE=electric",
	}
}

func managedElectricReplicationStreamID(session *localagent.Session) string {
	if session == nil {
		return ""
	}
	return managedPostgresDatabaseName("scenery", session.SessionID)
}

func managedElectricSlotName(streamID string) string {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return ""
	}
	return "electric_slot_" + streamID
}

func managedElectricPostgresApplicationName(root string, session *localagent.Session, streamID string) string {
	if session == nil {
		return ""
	}
	sessionID := strings.TrimSpace(session.SessionID)
	if sessionID == "" {
		return ""
	}
	baseAppID := strings.TrimSpace(session.BaseAppID)
	runtimeAppID := strings.TrimSpace(session.RuntimeAppID)
	if runtimeAppID == "" && baseAppID != "" {
		runtimeAppID = baseAppID + "--" + sessionID
	}
	if runtimeAppID == "" {
		runtimeAppID = sessionID
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return ""
	}
	return fmt.Sprintf("scenery-electric:%s:%s:%s:%s", appRootHash(root), shortIdentityHash(sessionID), shortIdentityHash(runtimeAppID), shortIdentityHash(streamID))
}

func shortIdentityHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func postgresURLWithApplicationName(raw, applicationName string) string {
	applicationName = strings.TrimSpace(applicationName)
	if applicationName == "" {
		return raw
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" {
		return raw
	}
	query := parsed.Query()
	query.Set("application_name", applicationName)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func cleanupManagedElectricStreamProcesses(ctx context.Context, root string, session *localagent.Session, streamID string) error {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return nil
	}
	processes, err := listManagedElectricStreamProcesses(streamID)
	if err != nil {
		return err
	}
	if len(processes) == 0 {
		return nil
	}
	var remaining []managedElectricStreamProcess
	for _, process := range processes {
		if process.PID <= 0 || process.PID == os.Getpid() {
			continue
		}
		if managedElectricProcessMatchesSession(process.Command, root, session, streamID) {
			if err := stopStaleSessionChildPID(ctx, process.PID); err != nil {
				remaining = append(remaining, process)
			}
			continue
		}
		remaining = append(remaining, process)
	}
	if len(remaining) == 0 {
		return nil
	}
	return fmt.Errorf("managed Electric stream %q is already owned by live process(es): %s", streamID, describeManagedElectricStreamProcesses(remaining))
}

func scanManagedElectricStreamProcesses(streamID string) ([]managedElectricStreamProcess, error) {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return nil, nil
	}
	output, err := exec.Command("ps", "-axo", "pid=,stat=,command=").Output()
	if err != nil {
		return nil, err
	}
	needle := "ELECTRIC_REPLICATION_STREAM_ID=" + streamID
	var matches []managedElectricStreamProcess
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, needle) || !commandContainsEnvValue(line, "ELECTRIC_REPLICATION_STREAM_ID", streamID) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || strings.Contains(fields[1], "Z") {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		matches = append(matches, managedElectricStreamProcess{
			PID:          pid,
			State:        fields[1],
			Command:      strings.Join(fields[2:], " "),
			Stream:       streamID,
			AppRoot:      commandEnvValue(line, "SCENERY_APP_ROOT"),
			SessionID:    commandEnvValue(line, "SCENERY_SESSION_ID"),
			RuntimeAppID: commandEnvValue(line, "SCENERY_RUNTIME_APP_ID"),
		})
	}
	return matches, nil
}

func managedElectricProcessMatchesSession(command, root string, session *localagent.Session, streamID string) bool {
	if session == nil {
		return false
	}
	runtimeAppID := strings.TrimSpace(session.RuntimeAppID)
	if runtimeAppID == "" && strings.TrimSpace(session.BaseAppID) != "" {
		runtimeAppID = strings.TrimSpace(session.BaseAppID) + "--" + strings.TrimSpace(session.SessionID)
	}
	return commandContainsEnvValue(command, "SCENERY_APP_ROOT", root) &&
		commandContainsEnvValue(command, "SCENERY_SESSION_ID", strings.TrimSpace(session.SessionID)) &&
		commandContainsEnvValue(command, "SCENERY_RUNTIME_APP_ID", runtimeAppID) &&
		commandContainsEnvValue(command, "SCENERY_DEV_SUPERVISOR", "1") &&
		commandContainsEnvValue(command, "SCENERY_ROLE", "electric") &&
		commandContainsEnvValue(command, "ELECTRIC_REPLICATION_STREAM_ID", strings.TrimSpace(streamID))
}

func commandContainsEnvValue(command, name, value string) bool {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" || value == "" {
		return false
	}
	got := commandEnvValue(command, name)
	if got == "" {
		return false
	}
	if name == "SCENERY_APP_ROOT" {
		return cleanAbsPath(got) == cleanAbsPath(value)
	}
	return strings.TrimSpace(got) == value
}

func commandEnvValue(command, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, part := range strings.Fields(command) {
		key, got, ok := strings.Cut(part, "=")
		if !ok || key != name {
			continue
		}
		return strings.TrimSpace(got)
	}
	return ""
}

func describeManagedElectricStreamProcesses(processes []managedElectricStreamProcess) string {
	if len(processes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(processes))
	for _, process := range processes {
		fullCommand := strings.TrimSpace(process.Command)
		command := fullCommand
		if len(command) > 220 {
			command = command[:220] + "..."
		}
		detail := fmt.Sprintf("pid=%d", process.PID)
		if strings.TrimSpace(process.State) != "" {
			detail += " state=" + strings.TrimSpace(process.State)
		}
		if strings.TrimSpace(process.Stream) != "" {
			detail += " stream=" + strconv.Quote(strings.TrimSpace(process.Stream))
		} else if stream := commandEnvValue(fullCommand, "ELECTRIC_REPLICATION_STREAM_ID"); stream != "" {
			detail += " stream=" + strconv.Quote(stream)
		}
		appRoot := firstNonEmpty(process.AppRoot, commandEnvValue(fullCommand, "SCENERY_APP_ROOT"))
		if appRoot != "" {
			detail += " app_root=" + strconv.Quote(appRoot)
		}
		sessionID := firstNonEmpty(process.SessionID, commandEnvValue(fullCommand, "SCENERY_SESSION_ID"))
		if sessionID != "" {
			detail += " session=" + strconv.Quote(sessionID)
		}
		runtimeAppID := firstNonEmpty(process.RuntimeAppID, commandEnvValue(fullCommand, "SCENERY_RUNTIME_APP_ID"))
		if runtimeAppID != "" {
			detail += " runtime_app=" + strconv.Quote(runtimeAppID)
		}
		if command != "" {
			detail += " cmd=" + strconv.Quote(command)
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, "; ")
}

func cleanupManagedElectricPostgresSlot(ctx context.Context, dbURL, root string, session *localagent.Session, streamID string) error {
	slotName := managedElectricSlotName(streamID)
	if strings.TrimSpace(dbURL) == "" || slotName == "" {
		return nil
	}
	applicationName := managedElectricPostgresApplicationName(root, session, streamID)
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}
	defer db.Close()

	cleanupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	locks, err := inspectElectricPostgresLocks(cleanupCtx, db, slotName)
	if err != nil {
		return err
	}
	if len(locks) == 0 {
		return nil
	}
	owned, blocked := classifyElectricPostgresLocksForCleanup(locks, applicationName)
	var failed []electricPostgresLock
	for _, lock := range owned {
		if lock.PID <= 0 {
			continue
		}
		terminated, err := terminatePostgresBackend(cleanupCtx, db, lock.PID)
		if err != nil || !terminated {
			failed = append(failed, lock)
		}
	}
	if len(blocked) > 0 {
		return fmt.Errorf("managed Electric slot %s has live non-owned contender(s) for stream %q: %s", slotName, strings.TrimSpace(streamID), describeElectricPostgresLocks(blocked))
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	defer waitCancel()
	for {
		remaining, err := inspectElectricPostgresLocks(waitCtx, db, slotName)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if len(failed) > 0 {
				remaining = failed
			}
			return fmt.Errorf("managed Electric slot %s is still held or waited on after stale cleanup: %s", slotName, describeElectricPostgresLocks(remaining))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func classifyElectricPostgresLocksForCleanup(locks []electricPostgresLock, applicationName string) (owned, blocked []electricPostgresLock) {
	applicationName = strings.TrimSpace(applicationName)
	for _, lock := range locks {
		if applicationName != "" && strings.TrimSpace(lock.Application) == applicationName {
			owned = append(owned, lock)
			continue
		}
		blocked = append(blocked, lock)
	}
	return owned, blocked
}

func inspectElectricPostgresLocks(ctx context.Context, db *sql.DB, slotName string) ([]electricPostgresLock, error) {
	slotName = strings.TrimSpace(slotName)
	if slotName == "" {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, electricPostgresLocksQuery, slotName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var locks []electricPostgresLock
	for rows.Next() {
		var lock electricPostgresLock
		if err := rows.Scan(&lock.PID, &lock.Kind, &lock.State, &lock.WaitEventType, &lock.WaitEvent, &lock.Query, &lock.Application, &lock.ClientAddr, &lock.SlotName); err != nil {
			return nil, err
		}
		if lock.Kind == "advisory-lock" && !textMentionsExactIdentifier(lock.Query, slotName) {
			continue
		}
		locks = append(locks, lock)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return locks, nil
}

const electricPostgresLocksQuery = `
WITH candidates AS (
	SELECT
		a.pid,
		'advisory-lock' AS kind,
		COALESCE(a.state, '') AS state,
		COALESCE(a.wait_event_type, '') AS wait_event_type,
		COALESCE(a.wait_event, '') AS wait_event,
		COALESCE(a.query, '') AS query,
		COALESCE(a.application_name, '') AS application_name,
		COALESCE(a.client_addr::text, '') AS client_addr,
		$1::text AS slot_name
	FROM pg_stat_activity a
	WHERE a.pid <> pg_backend_pid()
		AND a.query ILIKE '%' || $1::text || '%'
	UNION
	SELECT
		s.active_pid AS pid,
		'replication-slot' AS kind,
		COALESCE(a.state, '') AS state,
		COALESCE(a.wait_event_type, '') AS wait_event_type,
		COALESCE(a.wait_event, '') AS wait_event,
		COALESCE(NULLIF(a.query, ''), 'active replication slot ' || s.slot_name) AS query,
		COALESCE(a.application_name, '') AS application_name,
		COALESCE(a.client_addr::text, '') AS client_addr,
		s.slot_name::text
	FROM pg_replication_slots s
	LEFT JOIN pg_stat_activity a ON a.pid = s.active_pid
	WHERE s.active_pid IS NOT NULL
		AND s.slot_name::text = $1::text
)
SELECT DISTINCT pid, kind, state, wait_event_type, wait_event, query, application_name, client_addr, slot_name
FROM candidates
WHERE pid IS NOT NULL
ORDER BY pid, kind
`

func textMentionsExactIdentifier(text, identifier string) bool {
	text = strings.TrimSpace(text)
	identifier = strings.TrimSpace(identifier)
	if text == "" || identifier == "" {
		return false
	}
	offset := 0
	for {
		index := strings.Index(text[offset:], identifier)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(identifier)
		beforeOK := start == 0 || !isIdentifierByte(text[start-1])
		afterOK := end == len(text) || !isIdentifierByte(text[end])
		if beforeOK && afterOK {
			return true
		}
		offset = end
	}
}

func isIdentifierByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func terminatePostgresBackend(ctx context.Context, db *sql.DB, pid int) (bool, error) {
	var terminated bool
	if err := db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil {
		return false, err
	}
	return terminated, nil
}

func describeElectricPostgresLocks(locks []electricPostgresLock) string {
	if len(locks) == 0 {
		return "none"
	}
	var parts []string
	for _, lock := range locks {
		query := strings.Join(strings.Fields(lock.Query), " ")
		if len(query) > 160 {
			query = query[:160] + "..."
		}
		wait := strings.TrimSpace(strings.TrimSpace(lock.WaitEventType) + "/" + strings.TrimSpace(lock.WaitEvent))
		wait = strings.Trim(wait, "/")
		detail := fmt.Sprintf("pid=%d kind=%s state=%s", lock.PID, firstNonEmpty(lock.Kind, "unknown"), firstNonEmpty(lock.State, "unknown"))
		if wait != "" {
			detail += " wait=" + wait
		}
		if query != "" {
			detail += " query=" + strconv.Quote(query)
		}
		if strings.TrimSpace(lock.Application) != "" {
			detail += " application=" + strconv.Quote(strings.TrimSpace(lock.Application))
		}
		if strings.TrimSpace(lock.ClientAddr) != "" {
			detail += " client=" + strings.TrimSpace(lock.ClientAddr)
		}
		if strings.TrimSpace(lock.SlotName) != "" {
			detail += " slot=" + strconv.Quote(strings.TrimSpace(lock.SlotName))
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, "; ")
}

func waitForManagedElectric(ctx context.Context, service *managedElectricService) error {
	if service != nil && service.process != nil {
		return service.process.WaitReady(ctx, devProcessReadyRequest{
			Timeout:  30 * time.Second,
			Interval: 200 * time.Millisecond,
			Probe: func(context.Context) error {
				conn, err := net.DialTimeout("tcp", service.Addr, 200*time.Millisecond)
				if err != nil {
					return err
				}
				return conn.Close()
			},
		})
	}
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
	if s.process != nil {
		return s.process.Stop(5 * time.Second)
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

func (s *managedElectricService) PID() int {
	if s == nil || s.external {
		return 0
	}
	if s.process != nil {
		return s.process.PID
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
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
		if managedPostgresAdminReachableFn(ctx, adminURL) && postgresAdminVersionMatchesFn(ctx, adminURL, postgresServiceVersion(cfg)) {
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
	handle, reusable := (managedSubstrateManager{agent: agent}).reusable(ctx, postgresSubstrateAdapter{}, substrate)
	if !reusable {
		_, _ = agent.DeleteSubstrate(ctx, localagent.SubstratePostgres)
		return env
	}
	server, _ := handle.(*managedPostgresServer)
	if server == nil || strings.TrimSpace(server.AdminURL) == "" {
		return env
	}
	return append(append([]string(nil), env...), devPostgresAdminURLEnv+"="+server.AdminURL)
}

func ensureLocalManagedPostgresSubstrate(ctx context.Context, cfg app.Config, agent *localagent.Client) (string, error) {
	if agent == nil {
		return "", nil
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return "", err
	}
	root := filepath.Join(paths.AgentDir, "postgres")
	adapter := postgresSubstrateAdapter{cfg: cfg}
	handle, _, err := (managedSubstrateManager{agent: agent}).Ensure(ctx, root, adapter)
	if err != nil {
		return "", err
	}
	server, _ := handle.(*managedPostgresServer)
	if server == nil {
		return "", nil
	}
	(managedSubstrateManager{agent: agent}).Monitor(server, adapter)
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
	if err := localagent.VerifyOwner(owner); err != nil {
		return err
	}
	for name, pid := range substrate.PIDs {
		if pid <= 0 {
			continue
		}
		componentOwner := substrate.Owners[name]
		if componentOwner.PID <= 0 {
			componentOwner.PID = pid
		}
		if err := localagent.VerifyOwner(componentOwner); err != nil {
			return fmt.Errorf("substrate component %s owner invalid: %w", name, err)
		}
	}
	return nil
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
	binaries, err := resolveLocalPostgresBinaries(envpolicy.Environ())
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
	logPath := filepath.Join(root, "postgres.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
	cmd.Env = envpolicy.Environ()
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
		LogPath:   logPath,
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
	logPath := filepath.Join(root, "postgres-docker.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	containerName := managedPostgresContainerName(root, version, port)
	imageRef, err := managedToolchainImageRef("postgres", version)
	if err != nil {
		return nil, err
	}
	args := []string{
		"run", "--rm", "--pull", "missing",
		"--name", containerName,
		"-e", "POSTGRES_USER=scenery",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=postgres",
		"-p", fmt.Sprintf("127.0.0.1:%d:5432", port),
		"-v", dataDir + ":/var/lib/postgresql",
		imageRef,
	}
	args = append(args, managedPostgresServerArgs()...)
	cmd := commandTreeContext(context.Background(), docker, args...)
	configureDetachedChildProcess(cmd)
	configureCommandCancellation(cmd, 5*time.Second)
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = envpolicy.Environ()
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
		LogPath:  logPath,
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

func managedToolchainImageRef(name, version string) (string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", err
	}
	artifact, ok := manifest.Artifact(name)
	if !ok {
		return "", fmt.Errorf("toolchain image artifact %s is not declared", name)
	}
	if artifact.Version != version {
		return "", fmt.Errorf("toolchain image artifact %s has version %s, want %s", name, artifact.Version, version)
	}
	for _, image := range artifact.Images {
		if image.Digest != "" {
			return image.Ref + "@" + image.Digest, nil
		}
		if strings.TrimSpace(image.Ref) != "" {
			return image.Ref, nil
		}
	}
	return "", fmt.Errorf("toolchain image artifact %s has no image ref", name)
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
		return localPostgresBinaries{}, fmt.Errorf("managed Postgres needs explicit %s/%s or Docker; system PATH initdb/postgres are not used for managed toolchain artifacts", devPostgresInitDBEnv, devPostgresBinEnv)
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
	cmd := exec.CommandContext(initCtx, initdb, "-D", dataDir, "-U", "scenery", "-A", "trust")
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

func (server *managedPostgresServer) SubstrateRequest(ownerPID int) localagent.UpsertSubstrateRequest {
	if server == nil || strings.TrimSpace(server.AdminURL) == "" {
		return localagent.UpsertSubstrateRequest{}
	}
	if pid := serverPID(server); pid > 0 {
		ownerPID = pid
	}
	pids := map[string]int{}
	if ownerPID > 0 {
		pids["server"] = ownerPID
	}
	endpoints := map[string]string{
		"version":   firstNonEmpty(server.Version, devPostgresDefaultVersion),
		"isolation": devPostgresDefaultIsolation,
		"source":    firstNonEmpty(server.Source, "local-binary"),
	}
	if server.DataDir != "" {
		endpoints["data-dir"] = server.DataDir
	}
	if server.SocketDir != "" {
		endpoints["socket-dir"] = server.SocketDir
	}
	if server.Port > 0 {
		endpoints["port"] = fmt.Sprintf("%d", server.Port)
	}
	if server.LogPath != "" {
		endpoints["log"] = server.LogPath
	}
	return localagent.UpsertSubstrateRequest{
		Kind:      localagent.SubstratePostgres,
		Status:    "ready",
		OwnerPID:  ownerPID,
		PIDs:      pids,
		URLs:      map[string]string{"admin": server.AdminURL},
		Endpoints: endpoints,
	}
}

func (server *managedPostgresServer) MarkExternal() {}

func (server *managedPostgresServer) Components() []managedSubstrateComponent {
	if server == nil || server.done == nil {
		return nil
	}
	return []managedSubstrateComponent{{
		Name:        "server",
		DisplayName: "Postgres",
		Role:        "database",
		Done:        server.done,
		ExitRecord:  server.ExitRecord,
	}}
}

func (server *managedPostgresServer) ExitRecord(err error) localagent.SubstrateExit {
	if server == nil {
		return localagent.SubstrateExit{}
	}
	var state *os.ProcessState
	if server.cmd != nil {
		state = server.cmd.ProcessState
	}
	return substrateExitRecord("server", serverPID(server), time.Time{}, server.LogPath, server.LogPath, err, state)
}

type postgresSubstrateAdapter struct {
	cfg app.Config
}

func (a postgresSubstrateAdapter) Kind() string       { return localagent.SubstratePostgres }
func (a postgresSubstrateAdapter) SourceID() string   { return "postgres" }
func (a postgresSubstrateAdapter) SourceName() string { return "Postgres" }
func (a postgresSubstrateAdapter) Role() string       { return "database" }

func (a postgresSubstrateAdapter) Start(ctx context.Context, root string) (managedSubstrateHandle, error) {
	version := firstNonEmpty(strings.TrimSpace(postgresServiceVersion(a.cfg)), devPostgresDefaultVersion)
	return startLocalManagedPostgres(ctx, root, version)
}

func (a postgresSubstrateAdapter) FromSubstrate(ctx context.Context, substrate localagent.Substrate) (managedSubstrateHandle, bool) {
	adminURL := strings.TrimSpace(substrate.URLs["admin"])
	if adminURL == "" || !managedPostgresAdminReachableFn(ctx, adminURL) {
		return nil, false
	}
	version := firstNonEmpty(strings.TrimSpace(substrate.Endpoints["version"]), devPostgresDefaultVersion)
	requestedVersion := firstNonEmpty(strings.TrimSpace(postgresServiceVersion(a.cfg)), devPostgresDefaultVersion)
	if version != requestedVersion || !postgresAdminVersionMatchesFn(ctx, adminURL, requestedVersion) {
		return nil, false
	}
	port, _ := strconv.Atoi(strings.TrimSpace(substrate.Endpoints["port"]))
	return &managedPostgresServer{
		AdminURL:  adminURL,
		DataDir:   strings.TrimSpace(substrate.Endpoints["data-dir"]),
		SocketDir: strings.TrimSpace(substrate.Endpoints["socket-dir"]),
		Port:      port,
		Source:    strings.TrimSpace(substrate.Endpoints["source"]),
		Version:   version,
		LogPath:   strings.TrimSpace(substrate.Endpoints["log"]),
		ownerPID:  firstPositiveInt(substrate.OwnerPID, substrate.Owner.PID),
	}, true
}

func (a postgresSubstrateAdapter) ReadyFields(handle managedSubstrateHandle) map[string]any {
	server, _ := handle.(*managedPostgresServer)
	if server == nil {
		return nil
	}
	return map[string]any{
		"admin_url": server.AdminURL,
		"version":   server.Version,
		"source":    server.Source,
	}
}

func (a postgresSubstrateAdapter) ReuseFields(handle managedSubstrateHandle, _ localagent.Substrate) map[string]any {
	return a.ReadyFields(handle)
}

func (a postgresSubstrateAdapter) ExitStatus(managedSubstrateComponent) string {
	return "exited"
}

func (a postgresSubstrateAdapter) ExitMessage(managedSubstrateComponent) string {
	return "managed Postgres exited"
}

func (a postgresSubstrateAdapter) EventSource(_ managedSubstrateHandle, component managedSubstrateComponent, status string) devdash.DevSource {
	return devdash.DevSource{
		ID:     "postgres",
		Kind:   "substrate",
		Name:   "postgres",
		Role:   "database",
		Status: status,
		URL:    component.URL,
	}
}

func localPostgresAdminURL(socketDir string, port int) string {
	values := url.Values{}
	values.Set("host", socketDir)
	values.Set("port", fmt.Sprintf("%d", port))
	values.Set("sslmode", "disable")
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.User("scenery"),
		Path:     "/postgres",
		RawQuery: values.Encode(),
	}).String()
}

func localPostgresTCPAdminURL(port int) string {
	values := url.Values{}
	values.Set("sslmode", "disable")
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("scenery", "postgres"),
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
	// `docker info` can take several seconds on a loaded machine (e.g. CI
	// runners executing concurrent workflows); a short timeout misclassifies
	// a healthy daemon as unavailable.
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	return "scenery-postgres-" + hex.EncodeToString(sum[:])[:12]
}

func currentAgentSessionForAppRoot(ctx context.Context, appRoot string) (*localagent.Session, error) {
	client, err := localagent.DefaultClient()
	if err != nil {
		return nil, err
	}
	return currentAgentSessionForAppRootWithClient(ctx, client, appRoot)
}

func currentAgentSessionForAppRootWithClient(ctx context.Context, client *localagent.Client, appRoot string) (*localagent.Session, error) {
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no scenery dev runtime found for app root %s", appRoot)
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
		label = "scenery_session"
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

func externalPostgresDatabaseURL(env []string) (string, error) {
	if value, _ := lookupEnvValue(env, appDatabaseURLEnv); value != "" {
		return value, nil
	}
	if hasEnvValue(env, legacyDatabaseURLEnv) {
		return "", fmt.Errorf("%s=1 requires %s; %s is ignored as the Scenery app database authority", devPostgresExternalEnv, appDatabaseURLEnv, legacyDatabaseURLEnv)
	}
	return "", fmt.Errorf("%s=1 requires %s", devPostgresExternalEnv, appDatabaseURLEnv)
}

var ensureManagedPostgresDatabaseFn = ensureManagedPostgresDatabase
