package main

import (
	"context"

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
func runWorkerDatabaseSetup(ctx context.Context, root string, cfg app.Config, contract *compiler.Result, envName string) error {
	environment, err := cfg.ResolveEnv(envName)
	if err != nil {
		return err
	}
	return declaredDatabaseSetup{
		root: root, cfg: cfg, contract: contract,
		env: appChildEnv(envpolicy.Environ(), false,
			"SCENERY_APP_ID="+cfg.AppID(), "SCENERY_APP_ROOT="+root,
			"SCENERY_ENV="+environment.Name, "SCENERY_RUNTIME_ENV="+environment.Name),
	}.run(ctx)
}
