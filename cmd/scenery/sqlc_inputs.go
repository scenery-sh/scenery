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
	for _, block := range spec.Config.SQL {
		for _, query := range block.Queries.Values {
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: query, Suffix: ".sql"})
		}
		for _, schema := range block.Schema.Values {
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: schema, Suffix: ".sql"})
		}
	}
	for _, schema := range spec.Schemas {
		if schema.AtlasSource != "" {
			snapshot.excluded[filepath.ToSlash(filepath.Clean(schema.SQLCSchema))] = true
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: schema.AtlasSource})
		}
	}
	for _, plugin := range selectedSQLCPlugins(spec.Config) {
		if strings.HasPrefix(plugin.WASM.URL, "file://") {
			snapshot.selections = append(snapshot.selections, compiler.InputSelection{Path: strings.TrimPrefix(plugin.WASM.URL, "file://")})
		}
	}
	snapshot.capture, err = compiler.CaptureInputPaths(root, snapshot.selections, snapshot.excluded)
	if err != nil {
		return nil, err
	}
	configPath := filepath.ToSlash(filepath.Clean(firstNonEmpty(spec.ConfigPath, "sqlc.yaml")))
	if !bytes.Equal(snapshot.capture.Files[configPath], spec.ConfigBytes) {
		return nil, fmt.Errorf("revision_conflict: SQLC configuration changed after planning; retry the command")
	}
	return snapshot, nil
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
