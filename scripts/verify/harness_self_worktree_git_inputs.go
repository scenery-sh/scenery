package main

import (
	"fmt"
	"os"
	"path/filepath"
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
// declared Git-ignored input.
func checkHarnessWorktreeInputs(result worktreeCreateResult) error {
	if len(result.Inputs) != 1 || result.Inputs[0].Path != harnessWorktreeSeedInput {
		return fmt.Errorf("create %s received inputs %+v, want only the Git-ignored %s", result.Name, result.Inputs, harnessWorktreeSeedInput)
	}
	if data, err := os.ReadFile(filepath.Join(result.Path, harnessWorktreeSeedInput)); err != nil || string(data) != "id\n1\n" {
		return fmt.Errorf("create %s seed input = %q, %v", result.Name, data, err)
	}
	return nil
}
