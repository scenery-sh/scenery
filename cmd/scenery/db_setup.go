package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/postgresdb"
)

type dbSetupOptions struct {
	AppRoot string
	JSON    bool
}

type dbSetupResult struct {
	cliPayloadIdentity
	App   inspectdata.AppRef `json:"app"`
	Apply dbSetupPhase       `json:"apply"`
	Seed  dbSeedResult       `json:"seed"`
}

type dbSetupPhase struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func dbSetupCommand(args []string) error {
	return runDBSetup(context.Background(), os.Stdout, args)
}

func runDBSetup(ctx context.Context, stdout io.Writer, args []string) error {
	return runDBSetupWithHooks(ctx, stdout, args, defaultLifecycleHooks(), defaultDBSeedHooks())
}

func runDBSetupWithHooks(ctx context.Context, stdout io.Writer, args []string, lifecycle lifecycleHooks, seed dbSeedHooks) (returnErr error) {
	opts, err := parseDBSetupArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	contract, err := compileSQLContract(appRoot)
	if err != nil {
		return err
	}
	migrations, err := discoverDBMigrationPlans(appRoot, cfg, contract.SQLRequirements)
	if err != nil {
		return err
	}
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	env, closeOperation, err := beginDatabaseLifecycleEnv(ctx, appRoot, cfg, contract.SQLRequirements, env)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, closeOperation()) }()
	result := dbSetupResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.setup.result"),
		App:                buildDBApplyResult(appRoot, cfg).App,
		Apply:              dbSetupPhase{Status: "pending"},
		Seed: dbSeedResult{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.seed.result"),
			App:                buildDBApplyResult(appRoot, cfg).App,
			Seeds:              []dbSeedRecord{},
		},
	}

	if strings.TrimSpace(cfg.Database.Apply.Command) == "" && len(migrations) == 0 {
		result.Apply.Status = "skipped"
	} else {
		applyStdout := stdout
		if opts.JSON {
			applyStdout = io.Discard
		}
		var applyErr error
		if len(migrations) > 0 {
			_, applyErr = runDBMigrationPlans(ctx, cfg.AppID(), contract.SQLRequirements, migrations, env, postgresdb.SchemaMigrationOptions{})
		} else {
			applyErr = runDatabaseApplyCommandWithEnvIOHooks(ctx, appRoot, cfg.Database.Apply, env, applyStdout, os.Stderr, lifecycle)
		}
		if err := applyErr; err != nil {
			result.Apply.Status = "failed"
			result.Apply.Error = err.Error()
			if opts.JSON {
				return writeDBLifecycleJSON(stdout, result, err)
			} else {
				renderDBSetupText(stdout, result)
			}
			return err
		}
		result.Apply.Status = "applied"
	}

	seedResult, seedErr := buildDBSeedResultWithContractEnvHooks(ctx, appRoot, cfg, contract, dbSeedOptions{}, env, false, seed)
	result.Seed = seedResult
	if opts.JSON {
		return writeDBLifecycleJSON(stdout, result, seedErr)
	} else {
		renderDBSetupText(stdout, result)
	}
	return seedErr
}

func parseDBSetupArgs(args []string) (dbSetupOptions, error) {
	var opts dbSetupOptions
	flags := newCLIFlagSet("db setup")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbSetupOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return dbSetupOptions{}, err
	}
	return opts, nil
}

func renderDBSetupText(stdout io.Writer, result dbSetupResult) {
	if result.Apply.Status == "failed" {
		_, _ = fmt.Fprintf(stdout, "failed db apply: %s\n", result.Apply.Error)
		return
	}
	_, _ = fmt.Fprintln(stdout, "applied database setup")
	renderDBSeedText(stdout, result.Seed)
}
