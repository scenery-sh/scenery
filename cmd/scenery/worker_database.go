package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/storageconfig"
)

func workerDatabaseEnv(root string, cfg app.Config, environmentName string) []string {
	overrides := []string{
		"SCENERY_APP_ID=" + cfg.AppID(),
		"SCENERY_APP_ROOT=" + root,
		"SCENERY_ENV=" + environmentName,
		"SCENERY_RUNTIME_ENV=" + environmentName,
	}
	if storageCfg, ok := envpolicy.Lookup(storageconfig.RuntimeConfigEnv); ok && storageCfg != "" {
		overrides = append(overrides, storageconfig.RuntimeConfigEnv+"="+storageCfg)
	}
	return appChildEnv(workerProcessEnv(envpolicy.Environ(), "go"), false, overrides...)
}

// runWorkerDatabaseSetup applies the application's declared database setup
// before any worker starts, as the development supervisor does before it
// activates a candidate. No generation is live yet, so a pending migration is
// applied instead of only reported. The process environment already holds the
// endpoints managedDatabaseEnv resolved under the worktree's ownership checks
// and the storage capability a seed command may use.
func runWorkerDatabaseSetup(ctx context.Context, root string, cfg app.Config, contract *compiler.Result, envName string) error {
	environment, err := cfg.ResolveEnv(envName)
	if err != nil {
		return err
	}
	return declaredDatabaseSetup{
		root: root, cfg: cfg, contract: contract,
		env: workerDatabaseEnv(root, cfg, environment.Name),
	}.run(ctx)
}

// checkWorkerPendingMigrations reports pending schema migrations while a live
// worker generation is serving so reloads do not publish code against an
// unmigrated schema.
func checkWorkerPendingMigrations(ctx context.Context, root string, cfg app.Config, requirements compiler.SQLRequirements, envName string) (returnErr error) {
	if len(cfg.Database.Migrations) == 0 || len(requirements) == 0 {
		return nil
	}
	environment, err := cfg.ResolveEnv(envName)
	if err != nil {
		return err
	}
	migrations, err := discoverDBMigrationPlans(root, cfg, requirements)
	if err != nil || len(migrations) == 0 {
		return err
	}
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	results, err := runDBMigrationPlansWithDatabase(
		ctx,
		cfg.AppID(),
		requirements,
		migrations,
		workerDatabaseEnv(root, cfg, environment.Name),
		postgresdb.SchemaMigrationOptions{StatusOnly: true},
		connections,
	)
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Status != "current" {
			return &pendingMigrationError{Service: result.Service}
		}
	}
	return nil
}

func checkWorkerMigrationsCommand(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("invalid migration check request")
	}
	root, cfg, err := app.DiscoverRoot(args[0])
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	var requirements compiler.SQLRequirements
	if err := json.Unmarshal(raw, &requirements); err != nil {
		return err
	}
	return checkWorkerPendingMigrations(context.Background(), root, cfg, requirements, args[1])
}
