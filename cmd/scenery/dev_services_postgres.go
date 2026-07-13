package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

const (
	postgresServerStateKind  = "scenery.dev.postgres-server"
	postgresServerDescriptor = `{"identity":"artifact","state":"managed-postgres-server"}`
	postgresServerContainer  = "scenery-postgres"
	postgresServerVolume     = "scenery-postgres-data"
	postgresServerImage      = "postgres:18@sha256:4aabea78cf39b90e834caf3af7d602a18565f6fe2508705c8d01aa63245c2e20"
	postgresServerUser       = "scenery"
)

type postgresServerState struct {
	machine.ArtifactIdentity
	Container string    `json:"container"`
	Volume    string    `json:"volume,omitempty"`
	Image     string    `json:"image"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *postgresServerState) normalize() {
	if strings.TrimSpace(s.Container) == "" {
		s.Container = postgresServerContainer
	}
	if strings.TrimSpace(s.Volume) == "" {
		s.Volume = postgresServerVolume
	}
}

type postgresDockerRunner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

type execPostgresDockerRunner struct{}

func (execPostgresDockerRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return strings.TrimSpace(string(out)), fmt.Errorf("docker not found in PATH")
		}
		return strings.TrimSpace(string(out)), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

var (
	postgresDocker       postgresDockerRunner = execPostgresDockerRunner{}
	openPostgresDatabase                      = postgresdb.Open
	openPostgresAdmin                         = postgresdb.Open
	postgresReadyProbe                        = defaultPostgresReadyProbe
	postgresReadySleep                        = func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
)

func managedDatabaseEnv(ctx context.Context, appRoot string, cfg app.Config, session *localagent.Session, baseEnv []string) ([]string, postgresdb.Database, error) {
	return managedDatabaseEnvWithAgent(ctx, appRoot, cfg, session, nil, baseEnv)
}

func managedDatabaseEnvWithAgent(ctx context.Context, appRoot string, cfg app.Config, session *localagent.Session, agent *localagent.Client, baseEnv []string) ([]string, postgresdb.Database, error) {
	cfgs := cfg.DatabaseServices()
	if len(cfgs) == 0 {
		return nil, postgresdb.Database{}, nil
	}
	services := make([]postgresdb.Service, 0, len(cfgs))
	databaseEnv := appDatabaseURLEnv
	if value, _ := lookupEnvValue(baseEnv, databaseEnv); value != "" {
		if _, err := postgresdb.ParseURL(value); err != nil {
			return nil, postgresdb.Database{}, fmt.Errorf("%s must be a postgres URL for plan 0097: %w", databaseEnv, err)
		}
		for _, svc := range cfgs {
			serviceURL, err := postgresdb.ServiceURL(value, svc.Schema)
			if err != nil {
				return nil, postgresdb.Database{}, fmt.Errorf("derive postgres URL for service %s schema %s: %w", svc.Name, svc.Schema, err)
			}
			services = append(services, postgresdb.Service{
				Name:   svc.Name,
				Schema: svc.Schema,
				URL:    serviceURL,
			})
		}
		database := postgresdb.Database{Database: postgresdb.DatabaseNameFromURL(value), URL: value, Source: postgresdb.SourceExternal, Schemas: services}
		return postgresdb.Env(database), database, nil
	}

	server, err := ensureSharedPostgresServerWithAgent(ctx, appRoot, session, agent)
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	admin, err := openPostgresAdmin(ctx, server.databaseURL("postgres"))
	if err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("connect to managed postgres server: %w", err)
	}
	defer admin.Close()
	dbName := postgresname.DatabaseNameFor(cfg.AppID(), appRoot)
	if err := postgresdb.EnsureDatabase(ctx, admin, dbName); err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("ensure postgres database %s: %w", dbName, err)
	}
	baseURL := server.databaseURL(dbName)
	appDB, err := openPostgresDatabase(ctx, baseURL)
	if err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("connect to managed postgres database %s: %w", dbName, err)
	}
	defer appDB.Close()
	if err := postgresdb.EnsureSchema(ctx, appDB, "scenery"); err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("ensure postgres schema scenery: %w", err)
	}
	for _, svc := range cfgs {
		if err := postgresdb.EnsureSchema(ctx, appDB, svc.Schema); err != nil {
			return nil, postgresdb.Database{}, fmt.Errorf("ensure postgres schema %s for service %s: %w", svc.Schema, svc.Name, err)
		}
		serviceURL, err := postgresdb.ServiceURL(baseURL, svc.Schema)
		if err != nil {
			return nil, postgresdb.Database{}, fmt.Errorf("derive postgres URL for service %s schema %s: %w", svc.Name, svc.Schema, err)
		}
		services = append(services, postgresdb.Service{
			Name:   svc.Name,
			Schema: svc.Schema,
			URL:    serviceURL,
		})
	}
	database := postgresdb.Database{Database: dbName, URL: baseURL, Source: postgresdb.SourceManaged, Schemas: services}
	return postgresdb.Env(database), database, nil
}

func ensureSharedPostgresServer(ctx context.Context, appRoot string, session *localagent.Session) (*postgresServerState, error) {
	return ensureSharedPostgresServerWithAgent(ctx, appRoot, session, nil)
}

func ensureSharedPostgresServerWithAgent(ctx context.Context, appRoot string, session *localagent.Session, agent *localagent.Client) (*postgresServerState, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return nil, err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return nil, err
	}
	root := filepath.Join(paths.AgentDir, "postgres")
	unlock, err := lockManagedSubstrateRoot(root, "postgres")
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := loadOrCreatePostgresServerState(paths)
	if err != nil {
		return nil, err
	}
	if err := ensurePostgresDockerContainer(ctx, state); err != nil {
		return nil, err
	}
	if err := waitForPostgresServer(ctx, state); err != nil {
		return nil, err
	}
	_ = upsertPostgresSubstrateWithAgent(ctx, state, appRoot, session, agent)
	return state, nil
}

func loadOrCreatePostgresServerState(paths localagent.Paths) (*postgresServerState, error) {
	path := postgresServerStatePath(paths)
	state, err := loadPostgresServerState(path)
	if err == nil {
		return state, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	password, err := randomPostgresPassword()
	if err != nil {
		return nil, err
	}
	state = &postgresServerState{
		ArtifactIdentity: machine.NewArtifactIdentity(postgresServerStateKind, postgresServerDescriptor),
		Container:        postgresServerContainer,
		Volume:           postgresServerVolume,
		Image:            postgresServerImage,
		Port:             port,
		User:             postgresServerUser,
		Password:         password,
		CreatedAt:        time.Now().UTC(),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	return state, nil
}

func loadPostgresServerState(path string) (*postgresServerState, error) {
	var state postgresServerState
	if err := localagent.LoadDurableArtifact(path, &state, &state.ArtifactIdentity, postgresServerStateKind, postgresServerDescriptor, 0o600, func(fields map[string]json.RawMessage) error {
		var version string
		if err := json.Unmarshal(fields["schema_version"], &version); err != nil || version != "scenery.dev.postgres.server.v1" {
			return fmt.Errorf("unsupported legacy managed Postgres schema %q", version)
		}
		delete(fields, "schema_version")
		return nil
	}); err != nil {
		return nil, err
	}
	if state.Port <= 0 || state.Password == "" {
		return nil, fmt.Errorf("managed Postgres state %s is missing its port or password", path)
	}
	state.normalize()
	return &state, nil
}

func ensurePostgresDockerContainer(ctx context.Context, state *postgresServerState) error {
	status, err := postgresContainerStatus(ctx, state.Container)
	if err != nil {
		return err
	}
	switch status {
	case "running":
		return nil
	case "":
		_, err = postgresDocker.Run(ctx,
			"run", "-d",
			"--name", state.Container,
			"-p", fmt.Sprintf("127.0.0.1:%d:5432", state.Port),
			// postgres:18+ images require the volume at /var/lib/postgresql;
			// mounting the data subdirectory makes the entrypoint refuse to start.
			"-v", state.Volume+":/var/lib/postgresql",
			"-e", "POSTGRES_USER="+state.User,
			"-e", "POSTGRES_PASSWORD="+state.Password,
			state.Image,
		)
		return err
	default:
		_, err = postgresDocker.Run(ctx, "start", state.Container)
		return err
	}
}

func postgresContainerStatus(ctx context.Context, container string) (string, error) {
	out, err := postgresDocker.Run(ctx, "container", "inspect", container, "--format", "{{.State.Status}}")
	if err != nil {
		if isMissingDockerObject(out, err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func isMissingDockerObject(out string, err error) bool {
	msg := strings.ToLower(out)
	if err != nil {
		msg += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(msg, "no such object") ||
		strings.Contains(msg, "no such container") ||
		strings.Contains(msg, "no such volume") ||
		strings.Contains(msg, "not found")
}

func waitForPostgresServer(ctx context.Context, state *postgresServerState) error {
	deadline := time.Now().Add(30 * time.Second)
	delay := 50 * time.Millisecond
	var lastErr error
	for {
		if err := postgresReadyProbe(ctx, state); err == nil {
			return nil
		} else {
			lastErr = err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleepFor := delay
		if sleepFor > remaining {
			sleepFor = remaining
		}
		if err := postgresReadySleep(ctx, sleepFor); err != nil {
			return err
		}
		if delay < 500*time.Millisecond {
			delay *= 2
			if delay > 500*time.Millisecond {
				delay = 500 * time.Millisecond
			}
		}
	}
	if published, err := postgresContainerPublishedPort(ctx, state.Container); err == nil && published > 0 && published != state.Port {
		return fmt.Errorf("managed postgres container %q publishes port %d but the server state file expects port %d; the container was created from a different agent home or stale state — remove it with `docker rm -f %s` (data volume %q is preserved) and rerun, or restore the original agent home state: %w", state.Container, published, state.Port, state.Container, state.Volume, lastErr)
	}
	return fmt.Errorf("managed postgres server did not become ready: %w", lastErr)
}

func postgresContainerPublishedPort(ctx context.Context, container string) (int, error) {
	out, err := postgresDocker.Run(ctx, "container", "inspect", container, "--format", `{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}`)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return port, nil
}

func defaultPostgresReadyProbe(ctx context.Context, state *postgresServerState) error {
	db, err := openPostgresDatabase(ctx, state.databaseURL("postgres"))
	if err != nil {
		return err
	}
	_ = db.Close()
	return nil
}

func upsertPostgresSubstrateWithAgent(ctx context.Context, state *postgresServerState, appRoot string, session *localagent.Session, client *localagent.Client) error {
	var err error
	if client == nil {
		client, err = localagent.Ensure(ctx, cliBuildIdentity())
		if err != nil || client == nil {
			return err
		}
	}
	health, err := client.Health(ctx)
	if err != nil {
		return err
	}
	owner := localagent.CaptureOwner(health.PID, "scenery-postgres")
	leases := map[string]localagent.SubstrateLease{}
	if session != nil && strings.TrimSpace(session.SessionID) != "" {
		leaseOwner := session.Owner
		if leaseOwner.PID <= 0 {
			leaseOwner = localagent.CaptureOwner(firstPositiveInt(session.OwnerPID, os.Getpid()), "scenery-postgres-lease")
		}
		leases[session.SessionID] = localagent.SubstrateLease{
			SessionID: session.SessionID,
			AppRoot:   appRoot,
			URL:       state.publicURL(),
			OwnerPID:  leaseOwner.PID,
			Owner:     leaseOwner,
		}
	}
	_, err = client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstratePostgres,
		Status:   "ready",
		OwnerPID: owner.PID,
		Owner:    owner,
		URLs:     map[string]string{"server": state.publicURL()},
		Endpoints: map[string]string{
			"host": "127.0.0.1",
			"port": fmt.Sprint(state.Port),
		},
		Leases: leases,
	})
	return err
}

func (s *postgresServerState) databaseURL(database string) string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(s.User, s.Password),
		Host:   fmt.Sprintf("127.0.0.1:%d", s.Port),
		Path:   "/" + strings.Trim(database, "/"),
	}
	return u.String()
}

func (s *postgresServerState) publicURL() string {
	return postgresdb.RedactURL(s.databaseURL("postgres"))
}

func postgresServerStatePath(paths localagent.Paths) string {
	return filepath.Join(paths.AgentDir, "postgres", "server.json")
}

func randomPostgresPassword() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func emitPostgresReadyEvents(ctx context.Context, sink *devEventSink, database postgresdb.Database) {
	if sink == nil {
		return
	}
	for _, svc := range database.Schemas {
		sink.Emit(ctx, devdash.DevSource{ID: "postgres:" + svc.Name, Kind: "substrate", Name: svc.Name, Role: "database", Status: "running"}, "info", "Postgres service database ready", map[string]any{
			"service":  svc.Name,
			"engine":   "postgres",
			"database": database.Database,
			"schema":   svc.Schema,
			"source":   string(database.Source),
		})
	}
}
