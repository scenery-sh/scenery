package main

import (
	"context"
	"fmt"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

func validateSnapshotSchemas(schemas []snapshotManifestSchema) error {
	seen := map[string]bool{}
	for _, schema := range schemas {
		mapped, err := postgresname.SchemaNameFor(schema.Service)
		if err != nil || mapped != schema.Schema || len(schema.Schema) > 63 || seen[schema.Schema] {
			return fmt.Errorf("snapshot database schema %s=%s is not a unique service schema binding", schema.Service, schema.Schema)
		}
		seen[schema.Schema] = true
	}
	return nil
}

func configuredSnapshotDatabaseTarget(appRoot string, cfg appcfg.Config) (string, string, error) {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return "", "", err
	}
	if value := lookupEnvValue(env, appDatabaseURLEnv); strings.TrimSpace(value) != "" {
		if _, err := postgresdb.ParseURL(value); err != nil {
			return "", "", err
		}
		return postgresdb.DatabaseNameFromURL(value), string(postgresdb.SourceExternal), nil
	}
	return postgresname.DatabaseNameFor(cfg.AppID(), appRoot), string(postgresdb.SourceManaged), nil
}

func rejectSnapshotLiveSession(ctx context.Context, appRoot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := commandWorktreePaths(appRoot)
	if err != nil {
		return err
	}
	held, err := paths.ProbeLiveLock()
	if err != nil {
		return err
	}
	if held {
		return fmt.Errorf("snapshot requires the live dev runtime or restore operation to stop first; run scenery down --app-root %s", appRoot)
	}
	return nil
}

// The caller owns operation and storage capture locks. Never allocate a SQL
// resource merely to make a backup succeed; starting its verified owner is OK.
func resolveCaptureDatabaseHeld(ctx context.Context, root string, cfg appcfg.Config, op worktreePostgresOperation) (postgresdb.Database, error) {
	resolver, err := newWorktreePostgresResolver(ctx, root, cfg.AppID())
	if err != nil {
		return postgresdb.Database{}, err
	}
	server, err := resolver.ensureResourceWithOperation(ctx, op, false, false)
	if err != nil {
		return postgresdb.Database{}, err
	}
	database, err := databaseForWorktreeServer(resolver.paths.AppRoot, cfg, server, nil)
	if err != nil {
		return postgresdb.Database{}, err
	}
	return readActualDatabaseSchemas(ctx, database)
}

func requireSnapshotSchemas(ctx context.Context, database postgresdb.Database, schemas []snapshotManifestSchema) error {
	db, err := openPostgresDatabase(ctx, database.URL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	for _, schema := range schemas {
		var exists bool
		if err := db.QueryRowContext(ctx, `select exists(select 1 from pg_namespace where nspname = $1)`, schema.Schema).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("snapshot merge requires existing schema %q; run scenery db setup first", schema.Schema)
		}
	}
	return nil
}

func restoreSnapshotDatabase(ctx context.Context, archive *snapshotArchive, database postgresdb.Database, mode string) error {
	if mode == "overwrite" {
		admin, err := managedPostgresAdmin(ctx, database)
		if err != nil {
			return err
		}
		if err := postgresdb.DropDatabase(ctx, admin, database.Database); err != nil {
			_ = admin.Close()
			return err
		}
		if err := postgresdb.EnsureDatabase(ctx, admin, database.Database); err != nil {
			_ = admin.Close()
			return err
		}
		if err := admin.Close(); err != nil {
			return err
		}
	}
	runner, err := snapshotPostgresRunnerFor(database)
	if err != nil {
		return err
	}
	dump, err := archive.reader.Entry(archive.manifest.DB.DumpFile)
	if err != nil {
		return err
	}
	defer func() { _ = dump.Close() }()
	flags := []string{"--exit-on-error"}
	if mode == "merge" {
		flags = append(flags, "--data-only", "--single-transaction")
	}
	if err := runner.Restore(ctx, database, flags, dump); err != nil {
		return fmt.Errorf("restore snapshot database failed; rerun the same load to recover: %w", err)
	}
	return nil
}
