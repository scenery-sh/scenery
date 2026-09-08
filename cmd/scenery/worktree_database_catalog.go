package main

import (
	"context"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

// Snapshot access observes the selected actual resource. It deliberately does
// not compile desired source, so removal or invalidation cannot strand data.
func resolveSnapshotDatabase(ctx context.Context, root string, cfg appcfg.Config) (postgresdb.Database, error) {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
	if err != nil {
		return postgresdb.Database{}, err
	}
	if value := lookupEnvValue(env, appDatabaseURLEnv); value != "" {
		if err := validateAppPostgresURL(value); err != nil {
			return postgresdb.Database{}, err
		}
		return readActualDatabaseSchemas(ctx, postgresdb.Database{Database: postgresdb.DatabaseNameFromURL(value), URL: value, Source: postgresdb.SourceExternal})
	}
	resolver, err := newWorktreePostgresResolver(ctx, root, cfg.AppID())
	if err != nil {
		return postgresdb.Database{}, err
	}
	server, running, err := resolver.observe(ctx)
	if err != nil {
		return postgresdb.Database{}, err
	}
	if !running {
		return postgresdb.Database{}, worktreePostgresPrecondition("snapshot access requires the retained database to be running; no database was allocated")
	}
	database, err := databaseForWorktreeServer(resolver.paths.AppRoot, cfg, server, nil)
	if err != nil {
		return postgresdb.Database{}, err
	}
	return readActualDatabaseSchemas(ctx, database)
}

// The catalog is observed allocation data, not a second application model.
// pg_dump includes framework schemas; the service list excludes system and
// reserved framework namespaces, just as the prior manifest did.
func readActualDatabaseSchemas(ctx context.Context, database postgresdb.Database) (postgresdb.Database, error) {
	db, err := openPostgresDatabase(ctx, database.URL)
	if err != nil {
		return postgresdb.Database{}, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT nspname FROM pg_catalog.pg_namespace
WHERE nspname NOT IN ('information_schema', 'public', 'scenery') AND left(nspname, 3) <> 'pg_'
ORDER BY nspname`)
	if err != nil {
		return postgresdb.Database{}, err
	}
	defer func() { _ = rows.Close() }()
	database.Schemas = nil
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return postgresdb.Database{}, err
		}
		url, err := postgresdb.ServiceURL(database.URL, name)
		if err != nil {
			return postgresdb.Database{}, err
		}
		database.Schemas = append(database.Schemas, postgresdb.Service{Name: name, Schema: name, URL: url})
	}
	return database, rows.Err()
}
