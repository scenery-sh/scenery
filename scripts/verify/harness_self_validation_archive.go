package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Exercise the installed public boundary in an owned fixture, including dry
// selection, fail-fast omissions, immutable history and transitive ZIP export.
func runHarnessApplicationArchiveProof(ctx context.Context, repoRoot, appRoot string, artifacts harnessArtifactContext) (map[string]any, error) {
	config := `{"name":"validation-proof","envs":{"local":{"default":true}},"validation":{"default":"quick","profiles":{"quick":{"commands":[{"command":"sh","args":["-c","printf archive-proof"]}]},"failure":{"commands":[{"command":"sh","args":["-c","printf expected-failure >&2; exit 7"]},{"command":"sh","args":["-c","printf should-not-run"]}]}}}}`
	if err := writeHarnessValidationFile(filepath.Join(appRoot, ".scenery.json"), config); err != nil {
		return nil, err
	}
	if err := writeHarnessValidationFile(filepath.Join(appRoot, "go.mod"), "module archiveproof\n\ngo 1.26\n"); err != nil {
		return nil, err
	}
	binary := harnessLocalSceneryBinaryPath(repoRoot)
	commands := []harnessStep{}
	run := func(name string, args ...string) (map[string]any, error) {
		step := runHarnessExecStep(ctx, appRoot, name, append([]string{binary}, args...), artifacts)
		commands = append(commands, step)
		output := []byte(step.Evidence.StdoutTail)
		for _, reference := range step.Evidence.Artifacts {
			if reference.Name == sanitizeHarnessArtifactName(name+" stdout") {
				var err error
				output, err = os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(reference.Path)))
				if err != nil {
					return nil, err
				}
			}
		}
		var payload map[string]any
		if err := decodeCLIJSON(output, &payload); err != nil {
			return nil, fmt.Errorf("%s JSON: %w", name, err)
		}
		return payload, nil
	}
	if _, err := run("application archive success", "validate", "quick", "--write", "-o", "json"); err != nil {
		return nil, err
	}
	base := filepath.Join(appRoot, ".scenery", "harness", "validation")
	first, err := os.ReadFile(filepath.Join(base, "latest.json"))
	if err != nil {
		return nil, err
	}
	var firstResult map[string]any
	if err := json.Unmarshal(first, &firstResult); err != nil {
		return nil, err
	}
	firstRun := firstResult["run"].(map[string]any)["id"].(string)
	if firstResult["outcome"] != "passed" || firstResult["executed_steps"] != float64(1) {
		return nil, fmt.Errorf("successful validation did not execute its exact command")
	}
	if _, err := run("application archive dry selection", "validate", "quick", "--dry-run", "--write", "-o", "json"); err != nil {
		return nil, err
	}
	dry, err := harnessJSONFilePayload(filepath.Join(base, "latest.json"))
	if err != nil {
		return nil, err
	}
	if dry["outcome"] != "not_selected" || dry["dry_run"] != true || dry["executed_steps"] != float64(0) || len(dry["omitted_steps"].([]any)) != 1 {
		return nil, fmt.Errorf("dry-run history manufactured execution")
	}
	failed, err := run("application archive expected failure", "validate", "failure", "--write", "-o", "json")
	if err != nil {
		return nil, err
	}
	if failed["outcome"] != "failed" || failed["executed_steps"] != float64(1) || failed["selected_steps"] != float64(2) || failed["omitted_steps"].([]any)[0].(map[string]any)["outcome"] != "blocked" {
		return nil, fmt.Errorf("failed validation lost fail-fast selection/outcome")
	}
	old, err := os.ReadFile(filepath.Join(base, "runs", firstRun, "result.json"))
	if err != nil || !bytes.Equal(first, old) {
		return nil, fmt.Errorf("later validation overwrote immutable success: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(base, "runs"))
	if err != nil || len(entries) != 3 {
		return nil, fmt.Errorf("immutable application runs=%d: %v", len(entries), err)
	}
	for _, entry := range entries {
		for _, name := range []string{"plan", "result"} {
			payload, err := harnessJSONFilePayload(filepath.Join(base, "runs", entry.Name(), name+".json"))
			if err != nil {
				return nil, err
			}
			issues := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.validation."+name+".schema.json"), payload)
			if len(issues) > 0 {
				return nil, fmt.Errorf("archived validation %s schema: %v", name, issues)
			}
		}
	}
	zipPath := filepath.Join(appRoot, ".scenery", "harness", "export-proof.zip")
	export, err := run("application exact evidence export", "telemetry", "export", "--app-root", appRoot, "--output", zipPath, "-o", "json")
	if err != nil {
		return nil, err
	}
	if export["ok"] != true || len(export["runs"].([]any)) != 3 || len(export["files"].([]any)) < 8 {
		return nil, fmt.Errorf("export did not join three immutable plans/results with stdout/stderr")
	}
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, err
	}
	reference, err := artifacts.Write("application exact validation archive", "application-validation.zip", "", data)
	if err != nil {
		return nil, err
	}
	return map[string]any{"commands": commands, "archive": reference, "runs": 3, "proof": "exact_resolved_plan_success_dry_run_failure_blocked_tail_and_transitive_export"}, nil
}
