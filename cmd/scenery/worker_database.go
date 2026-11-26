package main

import (
	"context"
	"errors"

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
func runWorkerDatabaseSetup(ctx context.Context, root string, cfg app.Config, contract *compiler.Result, envName string) (fingerprint string, returnErr error) {
	environment, err := cfg.ResolveEnv(envName)
	if err != nil {
		return "", err
	}
	setup, hasWork, err := buildDevDatabaseSetup(root, cfg, contract)
	if err != nil || !hasWork {
		return "", err
	}
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	_, err = declaredDatabaseSetup{
		root: root, cfg: cfg, contract: contract,
		env: workerDatabaseEnv(root, cfg, environment.Name),
	}.apply(ctx, setup, connections)
	if err != nil {
		return "", err
	}
	return setup.Fingerprint, nil
}

// checkPendingMigrations ensures no pending schema migration exists before a live
// worker development rebuild starts candidate workers, without running apply or seeds.
func (dev *workerDevelopment) checkPendingMigrations(ctx context.Context) (setup devDatabaseSetup, changed bool, returnErr error) {
	if dev == nil || dev.watch == nil || dev.compiled == nil {
		return devDatabaseSetup{}, false, nil
	}
	_, cfg, err := app.DiscoverRoot(dev.watch.root)
	if err != nil {
		return devDatabaseSetup{}, false, err
	}
	environment, err := cfg.ResolveEnv(dev.envName)
	if err != nil {
		return devDatabaseSetup{}, false, err
	}
	setup, hasWork, err := buildDevDatabaseSetup(dev.watch.root, cfg, dev.compiled)
	if err != nil || !hasWork {
		return devDatabaseSetup{}, false, err
	}
	if setup.Fingerprint == dev.dbSetupFingerprint {
		return setup, false, nil
	}
	if dev.dbSetupApply != nil {
		if err := dev.dbSetupApply(ctx, setup, true); err != nil {
			return devDatabaseSetup{}, false, err
		}
		return setup, true, nil
	}
	if len(setup.Migrations) > 0 {
		connections := newDBSetupConnections()
		defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
		results, err := runDBMigrationPlansWithDatabase(ctx, cfg.AppID(), dev.compiled.SQLRequirements, setup.Migrations, workerDatabaseEnv(dev.watch.root, cfg, environment.Name), postgresdb.SchemaMigrationOptions{StatusOnly: true}, connections)
		if err != nil {
			return devDatabaseSetup{}, false, err
		}
		for _, result := range results {
			if result.Status != "current" {
				return devDatabaseSetup{}, false, &pendingMigrationError{Service: result.Service}
			}
		}
	}
	return setup, true, nil
}

// applyVerifiedDatabaseSetup runs database.apply and seeds after candidate
// workers have started and verified clean, right before publishing the generation.
func (dev *workerDevelopment) applyVerifiedDatabaseSetup(ctx context.Context, setup devDatabaseSetup) (returnErr error) {
	if dev == nil || dev.watch == nil || dev.compiled == nil {
		return nil
	}
	if dev.dbSetupApply != nil {
		return dev.dbSetupApply(ctx, setup, false)
	}
	_, cfg, err := app.DiscoverRoot(dev.watch.root)
	if err != nil {
		return err
	}
	environment, err := cfg.ResolveEnv(dev.envName)
	if err != nil {
		return err
	}
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	_, err = declaredDatabaseSetup{
		root: dev.watch.root, cfg: cfg, contract: dev.compiled, statusOnly: true,
		env: workerDatabaseEnv(dev.watch.root, cfg, environment.Name),
	}.apply(ctx, setup, connections)
	return err
}
