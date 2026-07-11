package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/vnext"
)

func writeMigrationTransitionPlan(path string, plan vnext.MigrationPlan) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return writeVNextCLIFile(path, append(encoded, '\n'))
}

func readMigrationTransitionPlan(path string) (vnext.MigrationPlan, error) {
	var plan vnext.MigrationPlan
	if err := readExactVNextPlanFile(path, "migration transition plan", &plan); err != nil {
		return vnext.MigrationPlan{}, err
	}
	return plan, nil
}

func readExactVNextPlanFile(path, description string, target any) error {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid_request: decode %s: %w", description, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid_request: decode %s: trailing JSON value", description)
		}
		return fmt.Errorf("invalid_request: decode %s trailing JSON: %w", description, err)
	}
	return nil
}
