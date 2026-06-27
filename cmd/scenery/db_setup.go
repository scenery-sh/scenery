package main

import (
	"context"
	"fmt"
	"io"
	"os"

	inspectdata "scenery.sh/internal/inspect"
)

type dbSetupOptions struct {
	AppRoot string
	JSON    bool
}

type dbSetupResult struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Apply         dbSetupPhase       `json:"apply"`
	Seed          dbSeedResult       `json:"seed"`
}

type dbSetupPhase struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func dbSetupCommand(args []string) error {
	return runDBSetup(context.Background(), os.Stdout, args)
}

func runDBSetup(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseDBSetupArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	result := dbSetupResult{
		SchemaVersion: "scenery.db.setup.result.v1",
		App:           buildDBApplyResult(appRoot, cfg).App,
		Apply:         dbSetupPhase{Status: "pending"},
		Seed: dbSeedResult{
			SchemaVersion: "scenery.db.seed.result.v1",
			App:           buildDBApplyResult(appRoot, cfg).App,
			Seeds:         []dbSeedRecord{},
		},
	}

	if err := runDatabaseApplyCommand(ctx, appRoot, cfg, cfg.Database.Apply); err != nil {
		result.Apply.Status = "failed"
		result.Apply.Error = err.Error()
		if opts.JSON {
			if writeErr := writeInspectJSON(stdout, result); writeErr != nil {
				return writeErr
			}
		} else {
			renderDBSetupText(stdout, result)
		}
		return err
	}
	result.Apply.Status = "applied"

	seedResult, seedErr := buildDBSeedResult(ctx, appRoot, cfg, dbSeedOptions{})
	result.Seed = seedResult
	if opts.JSON {
		if writeErr := writeInspectJSON(stdout, result); writeErr != nil {
			return writeErr
		}
	} else {
		renderDBSetupText(stdout, result)
	}
	return seedErr
}

func parseDBSetupArgs(args []string) (dbSetupOptions, error) {
	var opts dbSetupOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbSetupOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			return dbSetupOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func renderDBSetupText(stdout io.Writer, result dbSetupResult) {
	if result.Apply.Status == "failed" {
		fmt.Fprintf(stdout, "failed db apply: %s\n", result.Apply.Error)
		return
	}
	fmt.Fprintln(stdout, "applied database setup")
	renderDBSeedText(stdout, result.Seed)
}
