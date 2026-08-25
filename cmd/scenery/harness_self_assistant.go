package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
)

const harnessAssistantInitProbeName = "assistant initializer probe"

type harnessAssistantInitCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessAssistantInitProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessAssistantInitProbeStepWithCheck(ctx, repoRoot, runHarnessAssistantInitProbeCheck)
}

func runHarnessAssistantInitProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessAssistantInitCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessAssistantInitProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix production assistant initialization and predicted generation checks, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessAssistantInitProbeCheck(parent context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-assistant-init-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	appRoot := filepath.Join(root, "app")
	if err := copyHarnessNativeContractFixture(repoRoot, appRoot); err != nil {
		return nil, nil, err
	}
	compiledRoot, cfg, compiled, err := loadAssistantApp(appRoot)
	if err != nil {
		return nil, nil, err
	}
	response, err := initializeAssistant(ctx, compiledRoot, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api"})
	if err != nil {
		return nil, nil, err
	}
	if !response.Applied || response.Idempotent || len(response.Created) != 4 || response.PlanID == "" {
		return nil, nil, fmt.Errorf("assistant init response = %+v", response)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRoot, "docs", "schemas", "scenery.assistant.init.schema.json"), response); len(diagnostics) != 0 {
		return nil, nil, fmt.Errorf("assistant init schema validation failed: %s", strings.Join(diagnostics, "; "))
	}
	evalEntries, err := os.ReadDir(filepath.Join(appRoot, "assistants", "extra", "eval"))
	if err != nil {
		return nil, nil, err
	}
	if len(evalEntries) != 0 {
		return nil, nil, fmt.Errorf("assistant eval directory is not empty: %+v", evalEntries)
	}
	lock, err := os.ReadFile(filepath.Join(appRoot, "assistants", "extra", "package-lock.json"))
	if err != nil {
		return nil, nil, err
	}
	lockSum := sha256.Sum256(lock)
	lockDigest := "sha256:" + hex.EncodeToString(lockSum[:])
	if lockDigest != "sha256:50688be5a4ea2b73acffd21b724caa699ea81e8343befd22b1212e89e845938a" {
		return nil, nil, fmt.Errorf("assistant scaffold lock digest = %s", lockDigest)
	}
	appSource, err := os.ReadFile(filepath.Join(appRoot, "app.scn"))
	if err != nil {
		return nil, nil, err
	}
	if !strings.Contains(string(appSource), `assistant "extra"`) {
		return nil, nil, fmt.Errorf("canonical extra assistant block is missing")
	}
	after, err := compiler.Compile(appRoot)
	if err != nil {
		return nil, nil, err
	}
	if !after.Valid() {
		return nil, nil, fmt.Errorf("assistant-initialized graph is invalid: %#v", after.Diagnostics)
	}
	if _, ok := assistantResourceByKindAndName(after.Manifest, "extra", "scenery.assistant"); !ok {
		return nil, nil, fmt.Errorf("compiled assistant resource app/assistant/extra is missing")
	}
	return map[string]any{
		"proof":                        "production_assistant_init_planned_checked_and_applied_transactionally",
		"created_files":                response.Created,
		"plan_id":                      response.PlanID,
		"predicted_workspace_revision": response.PredictedWorkspaceRevision,
		"contract_revision":            after.Manifest.ContractRevision,
		"lock_digest":                  lockDigest,
	}, nil, nil
}
