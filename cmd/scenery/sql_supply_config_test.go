package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/postgresdb"
)

type sqlSupplyTestApp struct {
	t       *testing.T
	root    string
	home    string
	store   *appconfig.Store
	secrets *memorySecrets
}

func newSQLSupplyTestApp(t *testing.T) *sqlSupplyTestApp {
	t.Helper()
	home, root := t.TempDir(), t.TempDir()
	writeTestAppFile(t, root, app.PrimaryConfigFilename, `{"name":"shop","id":"shop","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["prod"]}}}}`)
	paths := localagent.PathsForHome(home)
	secrets := &memorySecrets{values: map[string][]byte{}}
	commandAgentPathsOverride = &paths
	configSecretBackendOverride = func(*appconfig.Store) (appconfig.SecretBackend, error) { return secrets, nil }
	appconfig.DurableFlush = func(*os.File) error { return nil }
	t.Cleanup(func() {
		commandAgentPathsOverride, configSecretBackendOverride = nil, nil
		appconfig.DurableFlush = (*os.File).Sync
	})
	t.Setenv(appDatabaseURLEnv, "postgres://ambient@db.example/ambient")
	t.Setenv(postgresdb.RegistryEnv, `{"url":"postgres://ambient@db.example/ambient"}`)
	store, err := appconfig.OpenStore(home, "shop")
	if err != nil {
		t.Fatal(err)
	}
	return &sqlSupplyTestApp{t: t, root: root, home: home, store: store, secrets: secrets}
}

func (a *sqlSupplyTestApp) configure(environment, url string) {
	a.t.Helper()
	version, err := a.secrets.Create(context.Background(), environment, appconfig.SQLDatabaseURLKey, []byte(url))
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.store.Mutate(context.Background(), environment, appconfig.Mutation{Key: appconfig.SQLDatabaseURLKey, Secret: &version}); err != nil {
		a.t.Fatal(err)
	}
}

func TestConfiguredSQLSupplyReplacesAmbientDatabaseURL(t *testing.T) {
	test := newSQLSupplyTestApp(t)

	// Unconfigured: the inherited variables no longer select external supply.
	if err := applyConfiguredSQLSupply(context.Background(), []string{"db", "list", "--app-root", test.root}); err != nil {
		t.Fatal(err)
	}
	for _, key := range ambientSQLSupplyKeys {
		if _, present := os.LookupEnv(key); present {
			t.Fatalf("ambient %s survived", key)
		}
	}

	// Configured for the default environment: db commands use it.
	test.configure("local", "postgres://shop@db.example/shop_local")
	if err := applyConfiguredSQLSupply(context.Background(), []string{"db", "list", "--app-root=" + test.root}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(appDatabaseURLEnv); got != "postgres://shop@db.example/shop_local" {
		t.Fatalf("DATABASE_URL = %q", got)
	}

	// A command that does not select SQL supply only loses the ambient value.
	if err := applyConfiguredSQLSupply(context.Background(), []string{"check", "--app-root", test.root}); err != nil {
		t.Fatal(err)
	}
	if _, present := os.LookupEnv(appDatabaseURLEnv); present {
		t.Fatal("check inherited SQL supply")
	}

	// Development runtimes refuse one external database shared by worktrees.
	err := applyConfiguredSQLSupply(context.Background(), []string{"up", "--app-root", test.root})
	if err == nil || !strings.Contains(err.Error(), "config unset sql.database_url --env local") {
		t.Fatalf("up with a local external database = %v", err)
	}
	if strings.Contains(err.Error(), "shop_local") {
		t.Fatal("the error revealed the configured URL")
	}
}

func TestConfiguredSQLSupplyFollowsTheDeploymentRoot(t *testing.T) {
	test := newSQLSupplyTestApp(t)
	test.configure("production", "not a url")
	test.configure("local", "postgres://shop@db.example/shop_local")

	stable := filepath.Join(test.home, "deployments", "shop", "production", "source")
	writeTestAppFile(t, stable, app.PrimaryConfigFilename, `{"name":"shop","id":"shop","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["prod"]}}}}`)
	if got := sqlSupplyEnvironment(stable, app.Config{ID: "shop"}, ""); got != "production" {
		t.Fatalf("stable root environment = %q", got)
	}
	if got := sqlSupplyEnvironment(test.root, app.Config{ID: "shop"}, ""); got != "" {
		t.Fatalf("worktree environment = %q", got)
	}
	// Without an installed release the desired production revision applies;
	// its invalid URL is reported without its value.
	err := applyConfiguredSQLSupply(context.Background(), []string{"db", "list", "--app-root", stable})
	if err == nil || !strings.Contains(err.Error(), "sql.database_url of environment production") || strings.Contains(err.Error(), "not a url") {
		t.Fatalf("invalid production URL = %v", err)
	}
}

func TestFlagValueReadsBothSpellings(t *testing.T) {
	args := []string{"--env", "a", "--app-root=/x", "--env=b", "--", "--env", "c"}
	if flagValue(args, "env") != "b" || flagValue(args, "app-root") != "/x" || flagValue(args, "missing") != "" {
		t.Fatalf("flag values = %q %q", flagValue(args, "env"), flagValue(args, "app-root"))
	}
}
