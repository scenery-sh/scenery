package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	devPostgresExternalEnv      = "SCENERY_DEV_POSTGRES_EXTERNAL"
	devZeroFSDefaultRoute       = "storage"
	devZeroFSToolchainArtifact  = "zerofs"
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

type managedZeroFSPlan struct {
	ServiceName   string
	StorageCellID string
	Route         string
	Image         string
	ToolchainDir  string
	CellRoot      string
	CacheDir      string
	ObjectsDir    string
	RunDir        string
	ConfigPath    string
	NinePListen   string
	NinePSocket   string
	RPCSocket     string
	WebUIListen   string
	WebUIAddrPath string
	LogPath       string
	Env           map[string]string
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

func shortIdentityHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
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
	if docker, dockerErr := execLookPath("docker"); dockerErr == nil {
		if dockerAvailable(ctx, docker) {
			return startLocalManagedPostgresContainer(ctx, root, version, docker)
		}
	}
	return nil, fmt.Errorf("managed Postgres needs Docker to run pinned image postgres:%s", version)
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

func dockerAvailable(ctx context.Context, docker string) bool {
	// `docker info` can take several seconds on a loaded machine (e.g. CI
	// runners executing concurrent workflows); a short timeout misclassifies
	// a healthy daemon as unavailable.
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(checkCtx, docker, "info").Run() == nil
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
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(dbName)); err != nil {
		if isPostgresDuplicateDatabaseRace(err) {
			return nil
		}
		return err
	}
	return nil
}

func isPostgresDuplicateDatabaseRace(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	switch string(pqErr.Code) {
	case "23505", "42P04":
		return true
	case "XX000":
		return strings.Contains(strings.ToLower(pqErr.Message), "tuple concurrently updated")
	default:
		return false
	}
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
