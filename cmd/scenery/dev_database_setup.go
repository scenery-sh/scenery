package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/postgresdb"
)

type devDatabaseSetup struct {
	Fingerprint string
	Seeds       []dbSeedPlan
	Migrations  []dbMigrationPlan
}

// devDatabaseSetupInputs is the database setup a contract's migration and seed
// bytes describe. The setup reads the contract's resources and SQL
// requirements, so it describes every contract of the same revision and
// requirements.
type devDatabaseSetupInputs struct {
	read     bool
	revision string
	sql      compiler.SQLRequirements
	setup    devDatabaseSetup
	hasWork  bool
	err      error
}

func (s *devSupervisor) readDevDatabaseSetupInputs(contract *compiler.Result) devDatabaseSetupInputs {
	setup, hasWork, err := buildDevDatabaseSetup(s.root, s.cfg, contract)
	inputs := devDatabaseSetupInputs{read: true, sql: contract.SQLRequirements, setup: setup, hasWork: hasWork, err: err}
	if contract.Manifest != nil {
		inputs.revision = contract.Manifest.ContractRevision
	}
	return inputs
}

// describes reports whether the inputs were read for a contract equal to
// contract in everything the setup reads.
func (inputs devDatabaseSetupInputs) describes(contract *compiler.Result) bool {
	return inputs.read && contract != nil && contract.Manifest != nil && inputs.revision != "" &&
		inputs.revision == contract.Manifest.ContractRevision && reflect.DeepEqual(inputs.sql, contract.SQLRequirements)
}

// nextDevDatabaseSetup decides whether the setup of inputs must run before the
// candidate is activated.
func (s *devSupervisor) nextDevDatabaseSetup(initial bool, inputs devDatabaseSetupInputs) (devDatabaseSetup, bool, error) {
	setup, hasWork, err := inputs.setup, inputs.hasWork, inputs.err
	if err != nil || !hasWork {
		return setup, false, err
	}
	if !initial && s.currentPID() != "" && setup.Fingerprint == s.dbSetupFingerprint {
		if s.console != nil && s.console.verbose {
			s.console.Event("database.setup.skip", map[string]any{
				"reason": "unchanged-inputs",
			})
		}
		return setup, false, nil
	}
	return setup, true, nil
}

func buildDevDatabaseSetup(root string, cfg app.Config, contract *compiler.Result) (devDatabaseSetup, bool, error) {
	var inputs []string
	applyCommand := strings.TrimSpace(cfg.Database.Apply.Command)
	if applyCommand != "" {
		data, err := json.Marshal(cfg.Database.Apply)
		if err != nil {
			return devDatabaseSetup{}, false, err
		}
		inputs = append(inputs, "apply:"+string(data))
	}
	migrations, err := discoverDBMigrationPlans(root, cfg, contract.SQLRequirements)
	if err != nil {
		return devDatabaseSetup{}, false, err
	}
	for _, plan := range migrations {
		for _, migration := range plan.Migrations {
			inputs = append(inputs, "migration:"+plan.Service+":"+migration.Path+":"+migration.Checksum)
		}
	}
	seeds, err := discoverDBSeedPlansForContract(root, cfg, "development", contract)
	if err != nil {
		return devDatabaseSetup{}, false, err
	}
	for _, seed := range seeds {
		inputs = append(inputs, "seed:"+seed.Path+":"+seed.SHA256)
	}
	if len(inputs) == 0 {
		return devDatabaseSetup{}, false, nil
	}
	sort.Strings(inputs)
	sum := sha256.Sum256([]byte(strings.Join(inputs, "\n")))
	return devDatabaseSetup{
		Fingerprint: hex.EncodeToString(sum[:]),
		Seeds:       seeds,
		Migrations:  migrations,
	}, true, nil
}

func (s *devSupervisor) runDevDatabaseSetup(ctx context.Context, setup devDatabaseSetup, contract *compiler.Result, environment *devRuntimeEnvironment) (returnErr error) {
	appBaseEnv := s.appDatabaseAuthorityEnv(environment.base, contract.SQLRequirements)
	env := appChildEnv(
		appBaseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"SCENERY_APP_ID="+s.activeAppID(),
		"SCENERY_APP_ROOT="+s.root,
		"SCENERY_ENV="+s.env.Name,
		"SCENERY_RUNTIME_ENV="+s.env.Name,
		"SCENERY_DEV_SUPERVISOR=1",
	)
	env = append(env, environment.managed...)
	env = append(env, managedDatabaseSetupEnv(contract.SQLRequirements, environment.managed)...)
	env = append(env, environment.storage...)
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	source := devdash.DevSource{ID: "database-setup", Kind: "setup", Name: "database setup", Role: "database", Status: "running"}
	s.eventSink().Emit(ctx, source, "info", "database setup started", map[string]any{
		"seed_count": len(setup.Seeds),
	})
	seedResult, err := declaredDatabaseSetup{
		root: s.root, cfg: s.cfg, contract: contract, env: env, console: s.console,
		// A live generation may still write against its old schema. Migration
		// status is safe here; actual evolution requires an explicit stopped
		// owner, and therefore cannot undermine failed-start rollback.
		statusOnly: s.currentPID() != "",
		failed: func(message string, err error) {
			source.Status = "error"
			s.eventSink().Emit(ctx, source, "error", message, map[string]any{"error": err.Error()})
		},
	}.apply(ctx, setup, connections)
	if err != nil {
		return err
	}
	source.Status = "ready"
	s.eventSink().Emit(ctx, source, "info", "database setup completed", map[string]any{
		"seeds":                seedResult.Summary,
		"migration_services":   len(setup.Migrations),
		"database_connections": connections.opened,
		"connection_reuses":    connections.reuses,
	})
	s.dbSetupFingerprint = setup.Fingerprint
	return nil
}

// declaredDatabaseSetup is one run of the database setup an application
// declares: schema migrations, then database.apply, then seeds, all against
// the endpoints in env. The development supervisor and the worker Host run it
// before the runtime that depends on those tables and seed data starts.
type declaredDatabaseSetup struct {
	root     string
	cfg      app.Config
	contract *compiler.Result
	env      []string
	console  *runConsole
	// statusOnly reports a pending migration instead of applying it.
	statusOnly bool
	// failed observes an apply or seed failure before the setup returns it.
	failed func(message string, err error)
	// lifecycle and seed replace process and database access in tests.
	lifecycle lifecycleHooks
	seed      dbSeedHooks
}

func (run declaredDatabaseSetup) apply(ctx context.Context, setup devDatabaseSetup, connections *dbSetupConnections) (dbSeedResult, error) {
	requirements := run.contract.SQLRequirements
	for _, plan := range setup.Seeds {
		dsn, err := resolveDatabaseURLForServiceFromEnv(requirements, run.env, plan.Service)
		if err != nil {
			return dbSeedResult{}, err
		}
		connections.retained[dsn] = true
	}
	if len(setup.Migrations) > 0 {
		results, err := runDBMigrationPlansWithDatabase(ctx, run.cfg.AppID(), requirements, setup.Migrations, run.env, postgresdb.SchemaMigrationOptions{StatusOnly: run.statusOnly}, connections)
		if err != nil {
			return dbSeedResult{}, err
		}
		for _, result := range results {
			if result.Status != "current" {
				return dbSeedResult{}, &pendingMigrationError{Service: result.Service}
			}
		}
	}
	failed := run.failed
	if failed == nil {
		failed = func(string, error) {}
	}
	if strings.TrimSpace(run.cfg.Database.Apply.Command) != "" {
		applyStdout := newSetupOutputWriter(run.console, "stdout", os.Stdout)
		applyStderr := newSetupOutputWriter(run.console, "stderr", os.Stderr)
		applyErr := runDatabaseApplyCommandWithEnvIOHooks(ctx, run.root, run.cfg.Database.Apply, run.env, applyStdout, applyStderr, run.lifecycle)
		applyStdout.Close()
		applyStderr.Close()
		if applyErr != nil {
			failed("database apply failed", applyErr)
			return dbSeedResult{}, applyErr
		}
	}
	hooks := run.seed
	if hooks.openStore == nil {
		hooks.openStore = connections.SeedStore
	}
	seedResult, err := buildDBSeedResultWithContractEnvHooks(ctx, run.root, run.cfg, run.contract, dbSeedOptions{}, run.env, false, hooks)
	if err != nil {
		failed("database seed failed", err)
	}
	return seedResult, err
}

// run discovers the declared setup and applies it over connections that live
// only for this invocation.
func (run declaredDatabaseSetup) run(ctx context.Context) (returnErr error) {
	setup, hasWork, err := buildDevDatabaseSetup(run.root, run.cfg, run.contract)
	if err != nil || !hasWork {
		return err
	}
	connections := newDBSetupConnections()
	defer func() { returnErr = errors.Join(returnErr, connections.Close()) }()
	_, err = run.apply(ctx, setup, connections)
	return err
}

func managedDatabaseSetupEnv(requirements compiler.SQLRequirements, managedEnv []string) []string {
	if len(requirements) == 0 {
		return nil
	}
	keys := databaseEnvKeys(requirements)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := envValueFromList(managedEnv, key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// pendingMigrationError reports a schema migration that a running supervisor
// cannot apply: building again cannot succeed until the migration inputs
// change or the migration is applied through down, db migrate and up.
type pendingMigrationError struct {
	Service string
}

func (e *pendingMigrationError) Error() string {
	return fmt.Sprintf("schema migration for %s is pending; the current runtime was retained; run scenery down, scenery db migrate, then scenery up", e.Service)
}

func (*pendingMigrationError) ExitCode() int { return 3 }
