package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/schemagen"
)

type dataGeneratorPlan struct {
	Record  generatorRecord
	Schemas []schemagen.ServiceSchema
}

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

func buildDataGeneratorPlan(appRoot string, appModel *model.App) (*dataGeneratorPlan, bool, error) {
	schemas, err := schemagen.Build(appRoot, appModel)
	if err != nil {
		return nil, false, err
	}
	if len(schemas) == 0 {
		return nil, false, nil
	}
	inputs := []string{".scenery.json", "**/*.go"}
	outputs := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		outputs = append(outputs, schema.GeneratedPath)
	}
	return &dataGeneratorPlan{
		Record: generatorRecord{
			ID:      "data",
			Kind:    "model-schema",
			Inputs:  uniqueSorted(inputs),
			Outputs: uniqueSorted(outputs),
			Tool:    "scenery-model-schema",
		},
		Schemas: schemas,
	}, true, nil
}

func appHasModelDirectives(appRoot string) bool {
	found := false
	_ = filepath.WalkDir(appRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".scenery", "node_modules", "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "//scenery:model") {
			found = true
		}
		return nil
	})
	return found
}

func writeGeneratedDataSchemas(appRoot string, schemas []schemagen.ServiceSchema) error {
	for _, schema := range schemas {
		if err := writeGeneratedFileIfChanged(filepath.Join(appRoot, filepath.FromSlash(schema.GeneratedPath)), []byte(schema.HCL)); err != nil {
			return err
		}
	}
	return nil
}

func writeGeneratedFileIfChanged(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && string(current) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func generatedSchemaDrift(appRoot string, schemas []schemagen.ServiceSchema) ([]schemagen.Drift, error) {
	return schemagen.Diff(appRoot, schemas, os.ReadFile, pathExists)
}

func buildGeneratedSchemaDiffResult(appRoot string, cfg appcfg.Config, appModel *model.App, schemas []schemagen.ServiceSchema, drift []schemagen.Drift) dbGeneratedDiffResult {
	records := make([]dbGeneratedSchemaRecord, 0, len(schemas))
	for _, schema := range schemas {
		records = append(records, dbGeneratedSchemaRecord{
			Service:       schema.Service,
			SourcePath:    schema.SourcePath,
			GeneratedPath: schema.GeneratedPath,
			Entities:      append([]string(nil), schema.Entities...),
		})
	}
	driftRecords := make([]dbGeneratedDriftRecord, 0, len(drift))
	for _, item := range drift {
		driftRecords = append(driftRecords, dbGeneratedDriftRecord{
			Service:       item.Service,
			SourcePath:    item.SourcePath,
			GeneratedPath: item.GeneratedPath,
			Message:       item.Message,
		})
	}
	return dbGeneratedDiffResult{
		SchemaVersion: "scenery.db.generated_diff.v1",
		OK:            len(driftRecords) == 0,
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".scenery.json"),
			ModulePath: appModel.ModulePath,
		},
		Drift:     driftRecords,
		Generated: records,
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
	schemas, err := schemagen.Build(appRoot, appModel)
	if err != nil {
		return err
	}
	drift, err := generatedSchemaDrift(appRoot, schemas)
	if err != nil {
		return err
	}
	result := buildGeneratedSchemaDiffResult(appRoot, cfg, appModel, schemas, drift)
	if opts.JSON {
		if err := writeInspectJSON(stdout, result); err != nil {
			return err
		}
		if !result.OK {
			return &silentCLIError{err: fmt.Errorf("generated schema drift detected")}
		}
		return nil
	}
	if len(schemas) == 0 {
		fmt.Fprintln(stdout, "scenery: no generated model schemas")
		return nil
	}
	if result.OK {
		fmt.Fprintf(stdout, "scenery: generated schema diff ok for %d service(s)\n", len(schemas))
		return nil
	}
	for _, item := range result.Drift {
		fmt.Fprintf(stdout, "%s: %s\n", item.Service, item.Message)
	}
	return fmt.Errorf("generated schema drift detected")
}

func parseDBGeneratedDiffArgs(args []string) (dbGeneratedDiffOptions, error) {
	var opts dbGeneratedDiffOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--generated":
			opts.Generated = true
		case "--app-root":
			i++
			if i >= len(args) {
				return dbGeneratedDiffOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			return dbGeneratedDiffOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}
