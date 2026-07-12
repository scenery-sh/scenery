package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
)

func TestManagedDatabaseEnvUsesExternalDSN(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {},
		}},
	}
	dsn := "postgres://user:secret@localhost/app"
	env, database, err := managedDatabaseEnv(t.Context(), root, cfg, nil, []string{"DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatalf("managedDatabaseEnv returned error: %v", err)
	}
	serviceURL := envValueFromList(env, "REPORTS_DATABASE_URL")
	if envValueFromList(env, "DATABASE_URL") != dsn || !strings.Contains(serviceURL, "search_path=reports%2Cscenery") {
		t.Fatalf("env = %+v", env)
	}
	if registry := envValueFromList(env, postgresdb.RegistryEnv); !strings.Contains(registry, `"source":"external"`) {
		t.Fatalf("registry = %q", registry)
	}
	if len(database.Schemas) != 1 || database.Schemas[0].Name != "reports" || database.Schemas[0].Schema != "reports" {
		t.Fatalf("database = %+v", database)
	}
}

func TestManagedDatabaseEnvUsesCanonicalAppURLEnv(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name:     "demo",
		Database: app.DatabaseConfig{},
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {},
		}},
	}
	dsn := "postgres://user:secret@localhost/app"
	env, _, err := managedDatabaseEnv(t.Context(), root, cfg, nil, []string{"DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatalf("managedDatabaseEnv returned error: %v", err)
	}
	if envValueFromList(env, "DATABASE_URL") != dsn || envValueFromList(env, "REPORTS_DATABASE_URL") == "" {
		t.Fatalf("env = %+v", env)
	}
}

type fakePostgresDockerRunner struct {
	calls [][]string
	run   func(args []string) (string, error)
}

func (f *fakePostgresDockerRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.run(args)
}

func TestPostgresContainerStatusTreatsNoSuchContainerAsMissing(t *testing.T) {
	oldDocker := postgresDocker
	t.Cleanup(func() { postgresDocker = oldDocker })
	postgresDocker = &fakePostgresDockerRunner{run: func(args []string) (string, error) {
		return "Error: No such container: scenery-postgres", errors.New("docker container inspect failed")
	}}

	status, err := postgresContainerStatus(context.Background(), postgresServerContainer)
	if err != nil {
		t.Fatalf("postgresContainerStatus returned error: %v", err)
	}
	if status != "" {
		t.Fatalf("status = %q, want missing", status)
	}
}

func TestCleanupPostgresHarnessContainerRemovesContainerAndVolume(t *testing.T) {
	oldDocker := postgresDocker
	t.Cleanup(func() { postgresDocker = oldDocker })
	fake := &fakePostgresDockerRunner{run: func(args []string) (string, error) {
		return "", nil
	}}
	postgresDocker = fake

	if err := cleanupPostgresHarnessContainer(context.Background(), "scenery-postgres-harness-test", "scenery-postgres-harness-test-data"); err != nil {
		t.Fatalf("cleanupPostgresHarnessContainer returned error: %v", err)
	}
	want := [][]string{
		{"rm", "-f", "scenery-postgres-harness-test"},
		{"volume", "rm", "scenery-postgres-harness-test-data"},
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("docker calls = %#v, want %#v", fake.calls, want)
	}
	for i := range want {
		if strings.Join(fake.calls[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("docker calls = %#v, want %#v", fake.calls, want)
		}
	}
}

func TestWaitForPostgresServerBacksOffFromFastPoll(t *testing.T) {
	oldProbe := postgresReadyProbe
	oldSleep := postgresReadySleep
	t.Cleanup(func() {
		postgresReadyProbe = oldProbe
		postgresReadySleep = oldSleep
	})
	var attempts int
	var sleeps []time.Duration
	postgresReadyProbe = func(context.Context, *postgresServerState) error {
		attempts++
		if attempts < 4 {
			return errors.New("not ready")
		}
		return nil
	}
	postgresReadySleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	err := waitForPostgresServer(context.Background(), &postgresServerState{User: "scenery", Password: "secret", Port: 5432})
	if err != nil {
		t.Fatalf("waitForPostgresServer returned error: %v", err)
	}
	want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", sleeps, want)
		}
	}
}

func TestValidateHeadlessPostgresEnvRequiresExplicitDSN(t *testing.T) {
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {},
		}},
	}
	err := validateHeadlessPostgresEnv(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "scenery up") {
		t.Fatalf("validateHeadlessPostgresEnv error = %v", err)
	}
	if err := validateHeadlessPostgresEnv(cfg, []string{"DATABASE_URL=postgres://user:secret@localhost/reports"}); err != nil {
		t.Fatalf("validateHeadlessPostgresEnv rejected explicit DSN: %v", err)
	}
}
