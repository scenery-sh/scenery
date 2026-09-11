package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/graph"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/postgresdb"
)

type dbMigrationOptions struct {
	AppRoot, Service           string
	Status, AdoptInitial, JSON bool
}

type dbMigrationPlan struct {
	Service, Schema     string
	Migrations          []postgresdb.SchemaMigration
	InitialVerification *postgresdb.SchemaVerification
}

type dbMigrationResult struct {
	cliPayloadIdentity
	App        inspectdata.AppRef                 `json:"app"`
	StatusOnly bool                               `json:"status_only"`
	Schemas    []postgresdb.SchemaMigrationResult `json:"schemas"`
}

var migrationFilename = regexp.MustCompile(`^([0-9]{4})_[a-z][a-z0-9_]*\.sql$`)

func dbMigrateCommand(args []string) error {
	return runDBMigrate(context.Background(), os.Stdout, args)
}

func parseDBMigrationArgs(args []string) (dbMigrationOptions, error) {
	var opts dbMigrationOptions
	flags := newCLIFlagSet("db migrate")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.Status, "status", false, "")
	flags.BoolVar(&opts.AdoptInitial, "adopt-initial", false, "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, err
	}
	if opts.Status && opts.AdoptInitial {
		return opts, fmt.Errorf("--status and --adopt-initial are mutually exclusive")
	}
	if len(positionals) > 1 {
		return opts, fmt.Errorf("usage: scenery db migrate [service] [--status | --adopt-initial] [--app-root <path>] [-o json]")
	}
	if len(positionals) == 1 {
		opts.Service = positionals[0]
	}
	return opts, nil
}

func runDBMigrate(ctx context.Context, stdout io.Writer, args []string) (returnErr error) {
	opts, err := parseDBMigrationArgs(args)
	if err != nil {
		return err
	}
	root, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	requirements, err := compileSQLRequirements(root)
	if err != nil {
		return err
	}
	plans, err := discoverDBMigrationPlans(root, cfg, requirements)
	if err != nil {
		return err
	}
	if opts.Service != "" {
		selected := plans[:0]
		for _, plan := range plans {
			if plan.Service == opts.Service {
				selected = append(selected, plan)
			}
		}
		plans = selected
	}
	if len(plans) == 0 {
		return fmt.Errorf("no database.migrations target is configured for this selection")
	}
	if opts.AdoptInitial {
		for _, plan := range plans {
			if plan.InitialVerification == nil {
				return fmt.Errorf("migration service %s has no app-authored initial_verification query; no database was changed", plan.Service)
			}
		}
	}
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
	if err != nil {
		return err
	}
	if opts.Status {
		database, err := resolvePostgresDatabaseFromEnv(ctx, root, cfg, requirements, env)
		if err != nil {
			return err
		}
		env = overlayEnv(envWithoutKeys(env, databaseEnvKeys(requirements)...), envMap(postgresdb.Env(database)))
	} else {
		var closeOperation func() error
		env, closeOperation, err = beginDatabaseLifecycleEnv(ctx, root, cfg, requirements, env)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, closeOperation()) }()
	}
	schemas, err := runDBMigrationPlans(ctx, cfg.AppID(), requirements, plans, env, postgresdb.SchemaMigrationOptions{StatusOnly: opts.Status, AdoptInitial: opts.AdoptInitial})
	result := dbMigrationResult{cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.migrate"), App: buildDBApplyResult(root, cfg).App, StatusOnly: opts.Status, Schemas: schemas}
	if opts.JSON {
		return writeDBLifecycleJSON(stdout, result, err)
	} else {
		for _, schema := range schemas {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%d migrations\n", schema.Service, schema.Status, len(schema.Migrations))
		}
	}
	return err
}

// A failed lifecycle still returns its structured evidence in one envelope.
// The silent error carries the exit code without printing a second JSON value.
func writeDBLifecycleJSON(stdout io.Writer, result any, resultErr error) error {
	var diagnostics []graph.Diagnostic
	if resultErr != nil {
		resultErr = &codedCLIError{code: 3, err: resultErr}
		diagnostics = []graph.Diagnostic{cliErrorDiagnostic(resultErr)}
	}
	if err := json.NewEncoder(stdout).Encode(newCLIEnvelope(resultErr == nil, result, diagnostics)); err != nil {
		return err
	}
	if resultErr != nil {
		return &silentCLIError{code: 3, err: resultErr}
	}
	return nil
}

// Discover all source inputs before allocation or SQL. Files are immutable
// within this invocation and contiguous by revision, not filesystem order.
func discoverDBMigrationPlans(root string, cfg appcfg.Config, requirements compiler.SQLRequirements) ([]dbMigrationPlan, error) {
	if len(cfg.Database.Migrations) > 256 {
		return nil, fmt.Errorf("database.migrations exceeds 256 service bindings")
	}
	plans := make([]dbMigrationPlan, 0, len(cfg.Database.Migrations))
	seen := map[string]bool{}
	for _, configured := range cfg.Database.Migrations {
		binding, ok := requirements.Binding(configured.Service)
		if !ok || binding.Schema == "scenery" || seen[binding.Schema] {
			return nil, fmt.Errorf("migration service %q must select one distinct application SQL binding", configured.Service)
		}
		seen[binding.Schema] = true
		directory, absolute, err := normalizeDBSeedWorkspacePath(root, configured.Directory, true)
		if err != nil {
			return nil, fmt.Errorf("migration service %s: %w", configured.Service, err)
		}
		entries, err := os.ReadDir(absolute)
		if err != nil {
			return nil, err
		}
		plan := dbMigrationPlan{Service: binding.Name, Schema: binding.Schema}
		if configured.InitialVerification != "" {
			path, absolute, err := normalizeDBSeedWorkspacePath(root, configured.InitialVerification, false)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(absolute)
			if err != nil || info.Size() > 4<<20 {
				return nil, fmt.Errorf("initial verification %s is unavailable or exceeds 4 MiB", path)
			}
			data, err := os.ReadFile(absolute)
			if err != nil {
				return nil, err
			}
			if err := postgresdb.ValidateInitialVerificationSQL(string(data)); err != nil {
				return nil, fmt.Errorf("initial verification %s: %w", path, err)
			}
			sum := sha256.Sum256(data)
			plan.InitialVerification = &postgresdb.SchemaVerification{Path: path, Checksum: "sha256:" + hex.EncodeToString(sum[:]), SQL: string(data)}
		}
		total := 0
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			matched := migrationFilename.FindStringSubmatch(entry.Name())
			if matched == nil {
				return nil, fmt.Errorf("migration filename %s must use 0001_description.sql", entry.Name())
			}
			revision, _ := strconv.Atoi(matched[1])
			if revision != len(plan.Migrations)+1 || revision > 256 {
				return nil, fmt.Errorf("migration files in %s must be contiguous from 0001, with at most 256 revisions", directory)
			}
			path, sourcePath, err := normalizeDBSeedWorkspacePath(root, filepath.Join(directory, entry.Name()), false)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(sourcePath)
			if err != nil || info.Size() > 4<<20 || int64(total)+info.Size() > 16<<20 {
				return nil, fmt.Errorf("migration %s exceeds the 4 MiB file or 16 MiB service bound", path)
			}
			data, err := os.ReadFile(sourcePath)
			if err != nil {
				return nil, err
			}
			total += len(data)
			if err := postgresdb.ValidateMigrationSQL(string(data)); err != nil {
				return nil, fmt.Errorf("migration %s: %w", path, err)
			}
			sum := sha256.Sum256(data)
			plan.Migrations = append(plan.Migrations, postgresdb.SchemaMigration{Revision: revision, Path: path, Checksum: "sha256:" + hex.EncodeToString(sum[:]), SQL: string(data)})
		}
		if len(plan.Migrations) == 0 {
			return nil, fmt.Errorf("migration service %s has no SQL files in %s", configured.Service, directory)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func runDBMigrationPlans(ctx context.Context, appID string, requirements compiler.SQLRequirements, plans []dbMigrationPlan, env []string, opts postgresdb.SchemaMigrationOptions) (_ []postgresdb.SchemaMigrationResult, returnErr error) {
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	return runDBMigrationPlansWithDatabase(ctx, appID, requirements, plans, env, opts, connections)
}

func runDBMigrationPlansWithDatabase(ctx context.Context, appID string, requirements compiler.SQLRequirements, plans []dbMigrationPlan, env []string, opts postgresdb.SchemaMigrationOptions, connections *dbSetupConnections) ([]postgresdb.SchemaMigrationResult, error) {
	results := make([]postgresdb.SchemaMigrationResult, 0, len(plans))
	for _, plan := range plans {
		dsn, err := resolveDatabaseURLForServiceFromEnv(requirements, env, plan.Service)
		if err != nil {
			return results, err
		}
		database, err := connections.Open(ctx, dsn)
		if err != nil {
			return results, fmt.Errorf("migration service %s database is unavailable; inspect scenery doctor and db server status", plan.Service)
		}
		opts.InitialVerification = plan.InitialVerification
		var result postgresdb.SchemaMigrationResult
		if !opts.StatusOnly && !opts.AdoptInitial {
			statusOptions := opts
			statusOptions.StatusOnly = true
			result, err = postgresdb.SchemaMigrations(ctx, database, appID, plan.Service, plan.Schema, plan.Migrations, statusOptions)
			if err == nil && result.Status != "current" {
				// Applying reacquires the exclusive transaction lock and rereads
				// the ledger; the status read is never mutation authority.
				result, err = postgresdb.SchemaMigrations(ctx, database, appID, plan.Service, plan.Schema, plan.Migrations, opts)
			}
		} else {
			result, err = postgresdb.SchemaMigrations(ctx, database, appID, plan.Service, plan.Schema, plan.Migrations, opts)
		}
		err = errors.Join(err, connections.ReleaseMigration(dsn))
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}
