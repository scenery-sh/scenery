package main

import (
	"context"
	"errors"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
)

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
		env: appChildEnv(envpolicy.Environ(), false,
			"SCENERY_APP_ID="+cfg.AppID(), "SCENERY_APP_ROOT="+root,
			"SCENERY_ENV="+environment.Name, "SCENERY_RUNTIME_ENV="+environment.Name),
	}.apply(ctx, setup, connections)
	if err != nil {
		return "", err
	}
	return setup.Fingerprint, nil
}

// verifyDatabaseSetup ensures no pending schema migration exists before a live
// worker development rebuild replaces the active generation.
func (dev *workerDevelopment) verifyDatabaseSetup(ctx context.Context) (fingerprint string, returnErr error) {
	if dev == nil || dev.watch == nil || dev.compiled == nil {
		return "", nil
	}
	_, cfg, err := app.DiscoverRoot(dev.watch.root)
	if err != nil {
		return "", err
	}
	environment, err := cfg.ResolveEnv(dev.envName)
	if err != nil {
		return "", err
	}
	setup, hasWork, err := buildDevDatabaseSetup(dev.watch.root, cfg, dev.compiled)
	if err != nil || !hasWork {
		return "", err
	}
	if setup.Fingerprint == dev.dbSetupFingerprint {
		return setup.Fingerprint, nil
	}
	if dev.dbSetupApply != nil {
		if err := dev.dbSetupApply(ctx, setup, true); err != nil {
			return "", err
		}
		return setup.Fingerprint, nil
	}
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	_, err = declaredDatabaseSetup{
		root: dev.watch.root, cfg: cfg, contract: dev.compiled, statusOnly: true,
		env: appChildEnv(envpolicy.Environ(), false,
			"SCENERY_APP_ID="+cfg.AppID(), "SCENERY_APP_ROOT="+dev.watch.root,
			"SCENERY_ENV="+environment.Name, "SCENERY_RUNTIME_ENV="+environment.Name),
	}.apply(ctx, setup, connections)
	if err != nil {
		return "", err
	}
	return setup.Fingerprint, nil
}
