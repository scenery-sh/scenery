package main

import (
	"fmt"
	"io"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/generateddata"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/schemagen"
)

type dataGeneratorPlan = generateddata.Plan

type dbGeneratedDiffOptions struct {
	AppRoot   string
	JSON      bool
	Generated bool
}

type dbGeneratedDiffResult struct {
	SchemaVersion string                    `json:"schema_version"`
	OK            bool                      `json:"ok"`
	App           inspectdata.AppRef        `json:"app"`
	Drift         []dbGeneratedDriftRecord  `json:"drift"`
	Generated     []dbGeneratedSchemaRecord `json:"generated"`
}

type dbGeneratedDriftRecord struct {
	Service       string `json:"service"`
	SourcePath    string `json:"source_path"`
	GeneratedPath string `json:"generated_path"`
	Message       string `json:"message"`
}

type dbGeneratedSchemaRecord struct {
	Service       string   `json:"service"`
	SourcePath    string   `json:"source_path"`
	GeneratedPath string   `json:"generated_path"`
	Entities      []string `json:"entities"`
}

func buildDataGeneratorPlan(appRoot string, cfg appcfg.Config, appModel *model.App) (*dataGeneratorPlan, bool, error) {
	return generateddata.Build(appRoot, cfg, appModel)
}

func appHasModelDirectives(appRoot string) bool {
	return generateddata.HasModelDirectives(appRoot)
}

func writeGeneratedDataArtifacts(appRoot string, plan *dataGeneratorPlan) error {
	return generateddata.Write(appRoot, plan)
}

func generatedSchemaDrift(appRoot string, schemas []schemagen.ServiceSchema) ([]schemagen.Drift, error) {
	return generateddata.Drift(appRoot, schemas)
}

func buildGeneratedSchemaDiffResult(appRoot string, cfg appcfg.Config, appModel *model.App, schemas []schemagen.ServiceSchema, drift []schemagen.Drift) dbGeneratedDiffResult {
	records := make([]dbGeneratedSchemaRecord, 0, len(schemas))
	for _, schema := range schemas {
		records = append(records, dbGeneratedSchemaRecord{
			Service: schema.Service, SourcePath: schema.SourcePath,
			GeneratedPath: schema.GeneratedPath, Entities: append([]string(nil), schema.Entities...),
		})
	}
	driftRecords := make([]dbGeneratedDriftRecord, 0, len(drift))
	for _, item := range drift {
		driftRecords = append(driftRecords, dbGeneratedDriftRecord{
			Service: item.Service, SourcePath: item.SourcePath,
			GeneratedPath: item.GeneratedPath, Message: item.Message,
		})
	}
	return dbGeneratedDiffResult{
		SchemaVersion: "scenery.db.generated_diff.v1",
		OK:            len(driftRecords) == 0,
		App: inspectdata.AppRef{
			Name: cfg.Name, ID: cfg.ID, Root: appRoot,
			ConfigPath: cfg.SourcePath(appRoot), ModulePath: appModel.ModulePath,
		},
		Drift: driftRecords, Generated: records,
	}
}

func runDBGeneratedDiff(stdout io.Writer, args []string) error {
	opts, err := parseDBGeneratedDiffArgs(args)
	if err != nil {
		return err
	}
	if !opts.Generated {
		return fmt.Errorf("missing --generated; expected: scenery db diff --generated [--app-root <path>] [--json]")
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	appModel, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return err
	}
	diff, err := generateddata.BuildDiff(appRoot, appModel)
	if err != nil {
		return err
	}
	result := buildGeneratedSchemaDiffResult(appRoot, cfg, appModel, diff.Schemas, diff.Drift)
	return renderDBGeneratedDiffResult(stdout, opts.JSON, result)
}

func renderDBGeneratedDiffResult(stdout io.Writer, jsonMode bool, result dbGeneratedDiffResult) error {
	if jsonMode {
		if err := writeInspectJSON(stdout, result); err != nil {
			return err
		}
		if !result.OK {
			return &silentCLIError{err: fmt.Errorf("generated schema drift detected")}
		}
		return nil
	}
	if len(result.Generated) == 0 {
		fmt.Fprintln(stdout, "scenery: no generated model schemas")
		return nil
	}
	if result.OK {
		fmt.Fprintf(stdout, "scenery: generated schema diff ok for %d service(s)\n", len(result.Generated))
		return nil
	}
	for _, item := range result.Drift {
		fmt.Fprintf(stdout, "%s: %s\n", item.Service, item.Message)
	}
	return fmt.Errorf("generated schema drift detected")
}

func parseDBGeneratedDiffArgs(args []string) (dbGeneratedDiffOptions, error) {
	var opts dbGeneratedDiffOptions
	flags := newCLIFlagSet("db diff")
	flags.BoolVar(&opts.Generated, "generated", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.JSON, "json", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbGeneratedDiffOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return dbGeneratedDiffOptions{}, err
	}
	return opts, nil
}
