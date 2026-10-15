package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
)

type sqlcInputSnapshot struct {
	capture    *compiler.InputCapture
	selections []compiler.InputSelection
	excluded   map[string]bool
}

func sqlcLocalPluginInputs(config sqlcConfigFile) []string {
	var paths []string
	for _, plugin := range selectedSQLCPlugins(config) {
		if strings.HasPrefix(plugin.WASM.URL, "file://") {
			paths = append(paths, strings.TrimPrefix(plugin.WASM.URL, "file://"))
		}
		if plugin.Process.LocalPath != "" {
			paths = append(paths, plugin.Process.LocalPath)
		}
	}
	return uniqueSorted(paths)
}

// Planning and execution share this read-only input capture. It neither starts
// tools nor creates staging, and only excludes currently authorized outputs.
func readSQLCInputSnapshot(root string, spec *sqlcGeneratorPlan) (*sqlcInputSnapshot, error) {
	managed, err := compiler.ManagedGeneratedRoots(spec.Contract)
	if err != nil {
		return nil, err
	}
	receipt, err := compiler.ReadSQLCArtifactReceipt(root)
	if err != nil {
		return nil, err
	}
	snapshot := &sqlcInputSnapshot{excluded: map[string]bool{}, selections: []compiler.InputSelection{{Path: firstNonEmpty(spec.ConfigPath, "sqlc.yaml")}}}
	for path := range receipt.Files {
		if sqlcPathInRoots(path, managed) {
			snapshot.excluded[path] = true
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".scenery.json")); err == nil {
		snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: ".scenery.json"})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var producedSchemas []string
	required := spec.InputSnapshot == nil
	for _, schema := range spec.Schemas {
		if schema.AtlasSource != "" {
			producedSchemas = append(producedSchemas, filepath.ToSlash(filepath.Clean(schema.SQLCSchema)))
		}
	}
	for _, block := range spec.Config.SQL {
		for _, query := range block.Queries.Values {
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: query, Suffix: ".sql", Required: required})
		}
		for _, schema := range block.Schema.Values {
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: schema, Suffix: ".sql", Required: required, Produced: producedSchemas})
		}
	}
	for _, schema := range spec.Schemas {
		if schema.AtlasSource != "" {
			snapshot.excluded[filepath.ToSlash(filepath.Clean(schema.SQLCSchema))] = true
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: schema.AtlasSource, Required: required})
		}
	}
	for _, path := range sqlcLocalPluginInputs(spec.Config) {
		snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: path, Required: required})
	}
	snapshot.capture, err = compiler.CaptureInputPaths(root, snapshot.selections, snapshot.excluded)
	if err != nil {
		return nil, err
	}
	if spec.InputSnapshot != nil && snapshot.capture.Digest != spec.InputRevision {
		return nil, fmt.Errorf("revision_conflict: SQLC inputs changed after capture; retry the command")
	}
	if err := validateSQLCPluginExecutables(root, spec.Config, spec.InputSnapshot != nil); err != nil {
		return nil, err
	}
	configPath := filepath.ToSlash(filepath.Clean(firstNonEmpty(spec.ConfigPath, "sqlc.yaml")))
	if !bytes.Equal(snapshot.capture.Files[configPath], spec.ConfigBytes) {
		return nil, fmt.Errorf("revision_conflict: SQLC configuration changed after planning; retry the command")
	}
	return snapshot, nil
}

func validateSQLCPluginExecutables(root string, config sqlcConfigFile, captured bool) error {
	classification := "capability_unavailable"
	if captured {
		classification = "revision_conflict"
	}
	for _, plugin := range selectedSQLCPlugins(config) {
		if path := plugin.Process.LocalPath; path != "" {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
			if os.IsNotExist(err) {
				return fmt.Errorf("%s: local SQLC process plugin is missing: %s", classification, path)
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("%s: local SQLC process plugin is not executable: %s", classification, path)
			}
		}
	}
	return nil
}

func (spec *sqlcGeneratorPlan) captureInputs(root string, contract *compiler.Result) error {
	spec.Contract = contract
	snapshot, err := readSQLCInputSnapshot(root, spec)
	if err != nil {
		return err
	}
	spec.InputSnapshot = snapshot
	spec.InputRevision = snapshot.capture.Digest
	return nil
}

func (spec *sqlcGeneratorPlan) verifyInputs(root string) error {
	current, err := readSQLCInputSnapshot(root, spec)
	if err != nil {
		return err
	}
	if current.capture.Digest != spec.InputRevision {
		return fmt.Errorf("revision_conflict: SQLC inputs changed before returning the plan; retry the command")
	}
	return nil
}
