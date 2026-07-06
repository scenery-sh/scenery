package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appdb "scenery.sh/db"
	"scenery.sh/internal/app"
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
				SuggestedAction: "Fix managed postgres service support, then rerun `scenery harness self --json --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessPostgresProbeCheck(parent context.Context, _ string) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if _, err := exec.LookPath("docker"); err != nil {
		return postgresProbeSkip("docker not found in PATH"), []checkDiagnostic{postgresProbeSkipDiagnostic("Docker CLI is unavailable")}, nil
	}
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}").CombinedOutput(); err != nil {
		return postgresProbeSkip(strings.TrimSpace(string(out))), []checkDiagnostic{postgresProbeSkipDiagnostic("Docker engine is unavailable")}, nil
	}
	initialContainerStatus, err := postgresContainerStatus(ctx, postgresServerContainer)
	if err != nil {
		return nil, nil, err
	}
	if initialContainerStatus == "" {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if cleanupErr := cleanupPostgresHarnessContainer(cleanupCtx); cleanupErr != nil {
				diagnostics = append(diagnostics, checkDiagnostic{
					Stage:           "postgres service probe",
					Severity:        "warning",
					Message:         "Disposable postgres harness container cleanup failed: " + cleanupErr.Error(),
					SuggestedAction: "Remove `scenery-postgres` and `scenery-postgres-data`, then rerun `scenery harness self --json --write`.",
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
	}
	agentHome := filepath.Join(os.TempDir(), "scenery-harness-postgres-"+harnessRandomLabel())
	defer os.RemoveAll(agentHome)
	restoreEnv := patchEnv(map[string]*string{
		"SCENERY_AGENT_HOME":    stringPtr(agentHome),
		"SCENERY_AGENT_DISABLE": nil,
		"SCENERY_APP_ROOT":      nil,
		"REPORTS_DATABASE_URL":  nil,
	})
	defer restoreEnv()
	rootA := filepath.Join(agentHome, "worktree-a")
	rootB := filepath.Join(agentHome, "worktree-b")
	cfg := app.Config{Name: "postgres-harness", ID: "postgres-harness", Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
		"reports": {Kind: "postgres"},
	}}}
	if err := writePostgresHarnessConfig(rootA); err != nil {
		return nil, nil, err
	}
	if err := writePostgresHarnessConfig(rootB); err != nil {
		return nil, nil, err
	}
	envA, servicesA, err := managedPostgresEnv(ctx, rootA, cfg, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	envB, servicesB, err := managedPostgresEnv(ctx, rootB, cfg, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if len(servicesA) != 1 || len(servicesB) != 1 {
		return nil, nil, fmt.Errorf("postgres harness expected one service per worktree")
	}
	check := func(ok bool, message string) {
		if !ok {
			diagnostics = append(diagnostics, checkDiagnostic{Stage: "postgres service probe", Severity: "error", Message: message})
		}
	}
	check(servicesA[0].Database != servicesB[0].Database, "two worktrees must get distinct postgres databases")
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
	if err := withPatchedEnvForDB(rootB, envB, func() error {
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
	admin, err := managedPostgresAdmin(ctx)
	if err != nil {
		return nil, diagnostics, err
	}
	defer admin.Close()
	if err := postgresdb.ResetDatabase(ctx, admin, servicesA[0].Database); err != nil {
		return nil, diagnostics, err
	}
	if err := assertPostgresMarkerGone(ctx, servicesA[0].URL); err != nil {
		return nil, diagnostics, err
	}
	_ = postgresdb.DropDatabase(ctx, admin, servicesA[0].Database)
	_ = postgresdb.DropDatabase(ctx, admin, servicesB[0].Database)
	containerMode := "existing"
	if initialContainerStatus == "" {
		containerMode = "disposable"
	}
	summary = map[string]any{
		"postgres_probe":   "ran",
		"container_status": firstNonEmpty(initialContainerStatus, "missing"),
		"container_mode":   containerMode,
		"database_a":       servicesA[0].Database,
		"database_b":       servicesB[0].Database,
		"diagnostics":      len(diagnostics),
	}
	if hasErrorDiagnostics(diagnostics) {
		return summary, diagnostics, fmt.Errorf("postgres service probe failed")
	}
	return summary, diagnostics, nil
}

func cleanupPostgresHarnessContainer(ctx context.Context) error {
	var messages []string
	if out, err := postgresDocker.Run(ctx, "rm", "-f", postgresServerContainer); err != nil && !isMissingDockerObject(out, err) {
		messages = append(messages, err.Error())
	}
	if out, err := postgresDocker.Run(ctx, "volume", "rm", postgresServerVolume); err != nil && !isMissingDockerObject(out, err) {
		messages = append(messages, err.Error())
	}
	if len(messages) > 0 {
		return fmt.Errorf("%s", strings.Join(messages, "; "))
	}
	return nil
}

func postgresProbeSkip(reason string) map[string]any {
	return map[string]any{"postgres_probe": "skipped", "reason": strings.TrimSpace(reason)}
}

func postgresProbeSkipDiagnostic(message string) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "postgres service probe",
		Severity:        "warning",
		Message:         message,
		SuggestedAction: "Start Docker and rerun `scenery harness self --json --write` for live postgres proof.",
	}
}

func writePostgresHarnessConfig(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"postgres-harness","id":"postgres-harness","dev":{"services":{"reports":{"kind":"postgres"}}}}`), 0o644)
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

func assertPostgresMarkerGone(ctx context.Context, dsn string) error {
	db, err := openPostgresDatabase(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var exists bool
	err = db.QueryRowContext(ctx, `select exists (select 1 from information_schema.tables where table_name = 'scenery_pg_marker')`).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("reset postgres database still has marker table")
	}
	return nil
}
