package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appdb "scenery.sh/db"
	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	durablestore "scenery.sh/internal/durable/store"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/postgresdb"
)

var runHarnessPostgresProbeCheckFunc = runHarnessPostgresProbeCheck

func runHarnessPostgresProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "postgres service probe",
		Command: []string{"scenery", "harness", "self", "internal:postgres-service-probe", repoRoot},
	}
	summary, diagnostics, err := runHarnessPostgresProbeCheckFunc(ctx, repoRoot)
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
				SuggestedAction: "Fix managed postgres service support, then rerun `scenery harness self -o json --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessPostgresProbeCheck(parent context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if _, err := exec.LookPath("docker"); err != nil {
		return postgresProbeSkip("docker not found in PATH"), []checkDiagnostic{postgresProbeSkipDiagnostic("Docker CLI is unavailable")}, nil
	}
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}").CombinedOutput(); err != nil {
		return postgresProbeSkip(strings.TrimSpace(string(out))), []checkDiagnostic{postgresProbeSkipDiagnostic("Docker engine is unavailable")}, nil
	}
	label := harnessRandomLabel()
	agentHome := filepath.Join(os.TempDir(), "scenery-harness-postgres-"+label)
	defer os.RemoveAll(agentHome)
	serverState, err := seedHarnessPostgresServerState(agentHome, label)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if cleanupErr := cleanupPostgresHarnessContainer(cleanupCtx, serverState.Container, serverState.Volume); cleanupErr != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "postgres service probe",
				Severity:        "warning",
				Message:         "Disposable postgres harness container cleanup failed: " + cleanupErr.Error(),
				SuggestedAction: "Remove `" + serverState.Container + "` and `" + serverState.Volume + "`, then rerun `scenery harness self -o json --write`.",
			})
			if summary != nil {
				summary["cleanup"] = "warning"
				summary["diagnostics"] = len(diagnostics)
			}
			return
		}
		if summary != nil {
			summary["cleanup"] = "removed_disposable_container"
		}
	}()
	restoreEnv := patchEnv(map[string]*string{
		"SCENERY_AGENT_HOME":    stringPtr(agentHome),
		"SCENERY_AGENT_DISABLE": nil,
		"SCENERY_APP_ROOT":      nil,
		"DATABASE_URL":          nil,
		"REPORTS_DATABASE_URL":  nil,
		"CACHE_DATABASE_URL":    nil,
	})
	defer restoreEnv()
	rootA := filepath.Join(agentHome, "worktree-a")
	rootB := filepath.Join(agentHome, "worktree-b")
	cfg := app.Config{Name: "postgres-harness", ID: "postgres-harness", Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
		"cache":   {},
		"reports": {},
	}}}
	if err := writePostgresHarnessConfig(rootA); err != nil {
		return nil, nil, err
	}
	if err := writePostgresHarnessConfig(rootB); err != nil {
		return nil, nil, err
	}
	envA, databaseA, err := managedDatabaseEnv(ctx, rootA, cfg, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	_, databaseB, err := managedDatabaseEnv(ctx, rootB, cfg, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if len(databaseA.Schemas) != 2 || len(databaseB.Schemas) != 2 {
		return nil, nil, fmt.Errorf("postgres harness expected two service schemas per worktree")
	}
	check := func(ok bool, message string) {
		if !ok {
			diagnostics = append(diagnostics, checkDiagnostic{Stage: "postgres service probe", Severity: "error", Message: message})
		}
	}
	check(databaseA.Database != "" && databaseA.URL != "", "managed postgres app database must be recorded")
	check(databaseA.Database != databaseB.Database, "two worktrees must get distinct postgres databases")
	appDB, err := openPostgresDatabase(ctx, databaseA.URL)
	if err != nil {
		return nil, diagnostics, err
	}
	defer appDB.Close()
	for _, schema := range []string{"scenery", "reports", "cache"} {
		ok, err := postgresSchemaExists(ctx, appDB, schema)
		if err != nil {
			return nil, diagnostics, err
		}
		check(ok, "postgres schema "+schema+" must exist")
	}
	if err := withPatchedEnvForDB(rootA, envA, func() error {
		db, err := appdb.Get(ctx, "reports")
		if err != nil {
			return err
		}
		defer appdb.Close("reports")
		_, err = db.ExecContext(ctx, `create table if not exists scenery_pg_marker(value text primary key); insert into scenery_pg_marker(value) values ('a') on conflict do nothing`)
		return err
	}); err != nil {
		return nil, diagnostics, err
	}
	if err := withPatchedEnvForDB(rootB, postgresdb.Env(databaseB), func() error {
		db, err := appdb.Get(ctx, "reports")
		if err != nil {
			return err
		}
		defer appdb.Close("reports")
		var exists bool
		err = db.QueryRowContext(ctx, `select exists (select 1 from information_schema.tables where table_name = 'scenery_pg_marker')`).Scan(&exists)
		if err != nil {
			return err
		}
		check(!exists, "second worktree database must not see first worktree marker table")
		return nil
	}); err != nil {
		return nil, diagnostics, err
	}
	if err := runPostgresHarnessDurableRoundTrip(ctx, databaseA.URL); err != nil {
		return nil, diagnostics, err
	}
	if err := runPostgresHarnessAuthBootstrap(ctx, repoRoot, appDB); err != nil {
		return nil, diagnostics, err
	}
	if _, err := appDB.ExecContext(ctx, `create table reports.reset_marker(id integer primary key); insert into reports.reset_marker values (1); create table cache.keep_marker(id integer primary key); insert into cache.keep_marker values (1)`); err != nil {
		return nil, diagnostics, err
	}
	if err := dbResetCommand([]string{"reports", "--app-root", rootA}); err != nil {
		return nil, diagnostics, err
	}
	reportsMarker, err := postgresTableExists(ctx, appDB, "reports", "reset_marker")
	if err != nil {
		return nil, diagnostics, err
	}
	cacheMarker, err := postgresTableExists(ctx, appDB, "cache", "keep_marker")
	if err != nil {
		return nil, diagnostics, err
	}
	check(!reportsMarker, "db reset reports must empty only the reports schema")
	check(cacheMarker, "db reset reports must leave the cache schema intact")
	admin, err := managedPostgresAdmin(ctx)
	if err != nil {
		return nil, diagnostics, err
	}
	defer admin.Close()
	_ = postgresdb.DropDatabase(ctx, admin, databaseA.Database)
	_ = postgresdb.DropDatabase(ctx, admin, databaseB.Database)
	summary = map[string]any{
		"postgres_probe": "ran",
		"container":      serverState.Container,
		"container_mode": "disposable",
		"database_a":     databaseA.Database,
		"database_b":     databaseB.Database,
		"schemas":        []string{"scenery", "reports", "cache"},
		"durable":        "roundtrip",
		"auth":           "bootstrap",
		"reset":          "service_schema_only",
		"diagnostics":    len(diagnostics),
	}
	if hasErrorDiagnostics(diagnostics) {
		return summary, diagnostics, fmt.Errorf("postgres service probe failed")
	}
	return summary, diagnostics, nil
}

func cleanupPostgresHarnessContainer(ctx context.Context, container, volume string) error {
	var messages []string
	if out, err := postgresDocker.Run(ctx, "rm", "-f", container); err != nil && !isMissingDockerObject(out, err) {
		messages = append(messages, err.Error())
	}
	if out, err := postgresDocker.Run(ctx, "volume", "rm", volume); err != nil && !isMissingDockerObject(out, err) {
		messages = append(messages, err.Error())
	}
	if len(messages) > 0 {
		return fmt.Errorf("%s", strings.Join(messages, "; "))
	}
	return nil
}

// seedHarnessPostgresServerState writes a server state file into an isolated
// harness agent home with harness-unique container and volume names, so the
// probe never contends with the machine-wide scenery-postgres container or
// with another harness step's server.
func seedHarnessPostgresServerState(agentHome, label string) (*postgresServerState, error) {
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	password, err := randomPostgresPassword()
	if err != nil {
		return nil, err
	}
	state := postgresServerState{
		ArtifactIdentity: machine.NewArtifactIdentity(postgresServerStateKind, postgresServerDescriptor),
		Container:        "scenery-postgres-harness-" + label,
		Volume:           "scenery-postgres-harness-" + label + "-data",
		Image:            postgresServerImage,
		Port:             port,
		User:             postgresServerUser,
		Password:         password,
		CreatedAt:        time.Now().UTC(),
	}
	path := postgresServerStatePath(localagent.PathsForHome(agentHome))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	return &state, nil
}

// harnessDockerAvailable reports whether the Docker CLI and engine are
// reachable, so harness steps can skip live Postgres checks cleanly.
func harnessDockerAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}").Run(); err != nil {
		return false
	}
	return true
}

func postgresProbeSkip(reason string) map[string]any {
	return map[string]any{"postgres_probe": "skipped", "reason": strings.TrimSpace(reason)}
}

func postgresProbeSkipDiagnostic(message string) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "postgres service probe",
		Severity:        "warning",
		Message:         message,
		SuggestedAction: "Start Docker and rerun `scenery harness self -o json --write` for live postgres proof.",
	}
}

func writePostgresHarnessConfig(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"postgres-harness","id":"postgres-harness","dev":{"services":{"reports":{},"cache":{}}}}`), 0o644)
}

func withPatchedEnvForDB(appRoot string, env []string, fn func() error) error {
	values := map[string]*string{"SCENERY_APP_ROOT": stringPtr(appRoot)}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = stringPtr(value)
		}
	}
	restore := patchEnv(values)
	defer restore()
	return fn()
}

func runPostgresHarnessDurableRoundTrip(ctx context.Context, databaseURL string) error {
	s, err := durablestore.Open(ctx, "reports", databaseURL, durablestore.Options{})
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.ReconcileTasks(ctx, []durablestore.TaskDeclaration{{Name: "reports.echo.v1", HandlerRef: "reports.echo.v1"}}); err != nil {
		return err
	}
	if _, err := s.Start(ctx, durablestore.StartRequest{ID: "harness-job", TaskName: "reports.echo.v1", InputBlob: []byte(`{"ok":true}`)}); err != nil {
		return err
	}
	leased, ok, err := s.LeaseReadyJob(ctx, "harness-worker", "harness-lease")
	if err != nil {
		return err
	}
	if !ok || leased.ID != "harness-job" {
		return fmt.Errorf("durable harness expected to lease harness-job, got %+v ok=%v", leased, ok)
	}
	return s.CompleteLeasedJob(ctx, leased.ID, "harness-worker", "harness-lease", []byte(`{"done":true}`))
}

func runPostgresHarnessAuthBootstrap(ctx context.Context, repoRoot string, db *sql.DB) error {
	data, err := os.ReadFile(filepath.Join(repoRoot, "auth", "db", "gen", "schema.sql"))
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, string(data)); err != nil {
		return err
	}
	ok, err := postgresTableExists(ctx, db, "scenery", "scenery_auth_users")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("standard auth bootstrap did not create scenery.scenery_auth_users")
	}
	return nil
}

func postgresSchemaExists(ctx context.Context, db *sql.DB, schema string) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `select exists (select 1 from information_schema.schemata where schema_name = $1)`, schema).Scan(&ok)
	return ok, err
}

func postgresTableExists(ctx context.Context, db *sql.DB, schema, table string) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `select exists (select 1 from information_schema.tables where table_schema = $1 and table_name = $2)`, schema, table).Scan(&ok)
	return ok, err
}
