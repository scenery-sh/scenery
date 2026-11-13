package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The seed input is declared and Git-ignored: only Scenery can carry it into
// a checkout Git creates. The tracked input shows that nothing else is.
const (
	harnessWorktreeAppConfig = `{"name":"demo","envs":{"local":{"default":true}},"database":{"seed":{"commands":[{"name":"catalog","service":"catalog","command":"true","inputs":["seed-data/catalog.csv","revision.txt"]}]}}}`
	harnessWorktreeSeedInput = "seed-data/catalog.csv"
)

func writeHarnessWorktreeSeedInput(appRoot string) error {
	if err := os.MkdirAll(filepath.Join(appRoot, "seed-data"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(appRoot, harnessWorktreeSeedInput), []byte("id\n1\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(appRoot, ".gitignore"), []byte("/seed-data/\n"), 0o644)
}

// checkHarnessWorktreeInputs proves that a created checkout received its
// declared Git-ignored input and that prepare supplies it again when lost.
func checkHarnessWorktreeInputs(ctx context.Context, repoRoot, appRoot string, result worktreeCreateResult) error {
	if len(result.Inputs) != 1 || result.Inputs[0].Path != harnessWorktreeSeedInput {
		return fmt.Errorf("create %s received inputs %+v, want only the Git-ignored %s", result.Name, result.Inputs, harnessWorktreeSeedInput)
	}
	if data, err := os.ReadFile(filepath.Join(result.Path, harnessWorktreeSeedInput)); err != nil || string(data) != "id\n1\n" {
		return fmt.Errorf("create %s seed input = %q, %v", result.Name, data, err)
	}
	// A checkout that lost the input receives it again and nothing else.
	if err := os.Remove(filepath.Join(result.Path, harnessWorktreeSeedInput)); err != nil {
		return err
	}
	var prepared bytes.Buffer
	if err := runProduct(ctx, repoRoot, &prepared, "worktree", "prepare", "--app-root", result.Path, "-o", "json"); err != nil {
		return err
	}
	var preparation worktreePrepareResult
	if err := decodeCLIJSON(prepared.Bytes(), &preparation); err != nil {
		return err
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.worktree.prepare.schema.json"), preparation); len(diagnostics) != 0 {
		return fmt.Errorf("prepare %s schema validation failed: %s", result.Name, strings.Join(diagnostics, "; "))
	}
	if !preparation.OK || preparation.Source != appRoot || len(preparation.Inputs) != 1 || preparation.Inputs[0].Path != harnessWorktreeSeedInput {
		return fmt.Errorf("prepare %s result = %+v", result.Name, preparation)
	}
	if _, err := os.Stat(filepath.Join(result.Path, harnessWorktreeSeedInput)); err != nil {
		return fmt.Errorf("prepare %s left the seed input absent: %w", result.Name, err)
	}
	return nil
}
