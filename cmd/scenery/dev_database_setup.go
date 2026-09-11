package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

func (s *devSupervisor) nextDevDatabaseSetup(initial bool, contract *compiler.Result) (devDatabaseSetup, bool, error) {
	setup, hasWork, err := buildDevDatabaseSetup(s.root, s.cfg, contract)
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
	for _, plan := range setup.Seeds {
		dsn, err := resolveDatabaseURLForServiceFromEnv(contract.SQLRequirements, env, plan.Service)
		if err != nil {
			return err
		}
		connections.retained[dsn] = true
	}
	source := devdash.DevSource{ID: "database-setup", Kind: "setup", Name: "database setup", Role: "database", Status: "running"}
	s.eventSink().Emit(ctx, source, "info", "database setup started", map[string]any{
		"seed_count": len(setup.Seeds),
	})
	if len(setup.Migrations) > 0 {
		// A live generation may still write against its old schema. Migration
		// status is safe here; actual evolution requires an explicit stopped
		// owner, and therefore cannot undermine failed-start rollback.
		statusOnly := s.currentPID() != ""
		results, err := runDBMigrationPlansWithDatabase(ctx, s.cfg.AppID(), contract.SQLRequirements, setup.Migrations, env, postgresdb.SchemaMigrationOptions{StatusOnly: statusOnly}, connections)
		if err != nil {
			return err
		}
		for _, result := range results {
			if result.Status != "current" {
				return fmt.Errorf("schema migration for %s is pending; the current runtime was retained; run scenery down, scenery db migrate, then scenery up", result.Service)
			}
		}
	}
	if strings.TrimSpace(s.cfg.Database.Apply.Command) != "" {
		applyStdout := newSetupOutputWriter(s.console, "stdout", os.Stdout)
		applyStderr := newSetupOutputWriter(s.console, "stderr", os.Stderr)
		applyErr := runDatabaseApplyCommandWithEnvIO(ctx, s.root, s.cfg.Database.Apply, env, applyStdout, applyStderr)
		applyStdout.Close()
		applyStderr.Close()
		if applyErr != nil {
			source.Status = "error"
			s.eventSink().Emit(ctx, source, "error", "database apply failed", map[string]any{
				"error": applyErr.Error(),
			})
			return applyErr
		}
	}
	hooks := defaultDBSeedHooks()
	hooks.openStore = connections.SeedStore
	seedResult, err := buildDBSeedResultWithContractEnvHooks(ctx, s.root, s.cfg, contract, dbSeedOptions{}, env, false, hooks)
	if err != nil {
		source.Status = "error"
		s.eventSink().Emit(ctx, source, "error", "database seed failed", map[string]any{
			"error": err.Error(),
		})
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
