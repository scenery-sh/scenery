// Package generateddata owns the lifecycle of artifacts derived from scenery
// model directives: planning, writing, and generated-schema drift detection.
package generateddata

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
	"scenery.sh/internal/schemagen"
	"scenery.sh/internal/webgen"
)

type Record struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Inputs  []string `json:"inputs,omitempty"`
	Outputs []string `json:"outputs,omitempty"`
	Tool    string   `json:"tool,omitempty"`
}

type Plan struct {
	Record     Record
	WebRecords []Record
	Schemas    []schemagen.ServiceSchema
	Seeds      []schemagen.ServiceSeed
	Web        []webgen.Bundle
}

type SchemaDiff struct {
	Schemas []schemagen.ServiceSchema
	Drift   []schemagen.Drift
}

func Build(appRoot string, cfg appcfg.Config, appModel *model.App) (*Plan, bool, error) {
	schemas, err := schemagen.Build(appRoot, appModel)
	if err != nil {
		return nil, false, err
	}
	seeds, err := schemagen.BuildSeeds(appRoot, appModel)
	if err != nil {
		return nil, false, err
	}
	web, err := webgen.Build(appRoot, appModel, cfg.Frontends)
	if err != nil {
		return nil, false, err
	}
	if len(schemas) == 0 && len(seeds) == 0 && len(web) == 0 {
		return nil, false, nil
	}
	configRel := cfg.SourceRelPath(appRoot)
	outputs := make([]string, 0, len(schemas)+len(seeds))
	for _, schema := range schemas {
		outputs = append(outputs, schema.GeneratedPath)
	}
	for _, seed := range seeds {
		outputs = append(outputs, seed.GeneratedPath)
	}
	webRecords := make([]Record, 0, len(web))
	for _, bundle := range web {
		bundleOutputs := make([]string, 0, len(bundle.Files))
		for _, file := range bundle.Files {
			bundleOutputs = append(bundleOutputs, file.Path)
		}
		webRecords = append(webRecords, Record{
			ID:      "web:" + bundle.Frontend,
			Kind:    "model-web",
			Inputs:  uniqueSorted([]string{configRel, "**/*.go", filepath.ToSlash(filepath.Join(bundle.FrontendRoot, "**/*.{ts,tsx}"))}),
			Outputs: uniqueSorted(bundleOutputs),
			Tool:    "scenery-model-webgen",
		})
	}
	return &Plan{
		Record: Record{
			ID:      "data",
			Kind:    "model-schema",
			Inputs:  uniqueSorted([]string{configRel, "**/*.go"}),
			Outputs: uniqueSorted(outputs),
			Tool:    "scenery-model-schema",
		},
		WebRecords: webRecords,
		Schemas:    schemas,
		Seeds:      seeds,
		Web:        web,
	}, true, nil
}

func HasModelDirectives(appRoot string) bool {
	found := false
	_ = filepath.WalkDir(appRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
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

func Write(appRoot string, plan *Plan) error {
	if plan == nil {
		return nil
	}
	for _, schema := range plan.Schemas {
		if err := writeIfChanged(filepath.Join(appRoot, filepath.FromSlash(schema.GeneratedPath)), []byte(schema.HCL)); err != nil {
			return err
		}
	}
	for _, seed := range plan.Seeds {
		if err := writeIfChanged(filepath.Join(appRoot, filepath.FromSlash(seed.GeneratedPath)), []byte(seed.SQL)); err != nil {
			return err
		}
	}
	for _, bundle := range plan.Web {
		for _, file := range bundle.Files {
			if err := writeIfChanged(filepath.Join(appRoot, filepath.FromSlash(file.Path)), []byte(file.Contents)); err != nil {
				return err
			}
		}
	}
	return nil
}

func Drift(appRoot string, schemas []schemagen.ServiceSchema) ([]schemagen.Drift, error) {
	return schemagen.Diff(appRoot, schemas, os.ReadFile, fileExists)
}

func BuildDiff(appRoot string, appModel *model.App) (SchemaDiff, error) {
	schemas, err := schemagen.Build(appRoot, appModel)
	if err != nil {
		return SchemaDiff{}, err
	}
	drift, err := Drift(appRoot, schemas)
	if err != nil {
		return SchemaDiff{}, err
	}
	return SchemaDiff{Schemas: schemas, Drift: drift}, nil
}

func writeIfChanged(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && string(current) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}
