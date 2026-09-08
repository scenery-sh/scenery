package main

import (
	"context"
	"errors"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

func requireManagedDatabaseSelection(root string) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
	if err != nil {
		return err
	}
	if lookupEnvValue(env, appDatabaseURLEnv) != "" {
		return worktreePostgresPrecondition("DATABASE_URL is external; refusing managed database cleanup")
	}
	return nil
}

// Standalone apply/seed owns the worktree until all SQL and child commands
// finish. External capabilities have no Scenery lifecycle to lock.
func beginDatabaseLifecycleEnv(ctx context.Context, root string, cfg appcfg.Config, requirements compiler.SQLRequirements, baseEnv []string) (_ []string, _ func() error, returnErr error) {
	noop := func() error { return nil }
	bindings, err := resolveSQLSupply(requirements, baseEnv, true)
	if err != nil {
		return nil, nil, err
	}
	if len(bindings) == 0 || lookupEnvValue(baseEnv, appDatabaseURLEnv) != "" {
		env, err := managedDatabaseLifecycleEnv(ctx, root, cfg, requirements, baseEnv)
		return env, noop, err
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return nil, nil, err
	}
	live, err := paths.AcquireLiveLock()
	if err != nil {
		return nil, nil, worktreePostgresPrecondition("a live owner or database operation prevents standalone apply/seed; use down first")
	}
	success := false
	defer func() {
		if !success {
			returnErr = errors.Join(returnErr, live.Release())
		}
	}()
	env, err := managedDatabaseLifecycleEnv(ctx, root, cfg, requirements, baseEnv)
	if err != nil {
		return nil, nil, err
	}
	op, err := paths.BeginOperation()
	if err != nil {
		return nil, nil, err
	}
	success = true
	return env, func() error { return errors.Join(op.Close(), live.Release()) }, nil
}

// Destructive app-database commands own both locks for their complete SQL
// operation. A stopped existing engine may be started, but is never allocated.
func beginInactiveDatabaseOperation(ctx context.Context, root string, cfg appcfg.Config) (_ postgresdb.Database, _ func() error, returnErr error) {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	if lookupEnvValue(env, appDatabaseURLEnv) != "" {
		return postgresdb.Database{}, nil, worktreePostgresPrecondition("DATABASE_URL is external; refusing managed database mutation")
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	if _, err := paths.LoadRecord(cfg.AppID()); err != nil {
		return postgresdb.Database{}, nil, err
	}
	live, err := paths.AcquireLiveLock()
	if err != nil {
		return postgresdb.Database{}, nil, worktreePostgresPrecondition("a live owner or restore operation prevents database mutation; use down first")
	}
	success := false
	defer func() {
		if !success {
			returnErr = errors.Join(returnErr, live.Release())
		}
	}()
	resolver, err := newWorktreePostgresResolver(ctx, paths.AppRoot, cfg.AppID())
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	op, err := resolver.beginOperation()
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	wasRunning := false
	closeOperation := func() error {
		closeErr := op.Close()
		if !wasRunning {
			stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			closeErr = errors.Join(closeErr, resolver.stop(stopCtx))
			cancel()
		}
		return errors.Join(closeErr, live.Release())
	}
	defer func() {
		if !success {
			returnErr = errors.Join(returnErr, closeOperation())
		}
	}()
	record, err := resolver.load()
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	_, container, err := resolver.inspect(ctx, record)
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	wasRunning = container != nil && container.Running
	server, err := resolver.ensureWithOperation(ctx, op, false)
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	database, err := databaseForWorktreeServer(paths.AppRoot, cfg, server, nil)
	if err != nil {
		return postgresdb.Database{}, nil, err
	}
	success = true
	return database, closeOperation, nil
}
