package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

// isolateVerification owns the fallback home for every descendant in this lane.
// Probe-specific homes can narrow it further. Persist command evidence alongside
// the lane's artifacts before removing disposable runtime state.
func isolateVerification(repoRoot, runID, mode string) (func(), error) {
	parent := filepath.Join(repoRoot, ".scenery", "harness")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp(parent, "verification-agent-")
	if err != nil {
		return nil, err
	}
	purpose := "verification"
	if mode == harnessSelfModeRelease || envpolicy.Get("SCENERY_EXECUTION_PURPOSE") == "release" {
		purpose = "release"
	}
	restore := patchEnv(map[string]*string{
		"SCENERY_AGENT_HOME":        stringPtr(home),
		"SCENERY_AGENT_SOCKET":      nil,
		"SCENERY_AGENT_ROUTER_ADDR": nil,
		"SCENERY_EXECUTION_PURPOSE": stringPtr(purpose),
	})
	return func() {
		restore()
		if data, readErr := os.ReadFile(filepath.Join(home, "telemetry.jsonl")); readErr == nil {
			dir := filepath.Join(parent, "artifacts", runID)
			if mkdirErr := os.MkdirAll(dir, 0o700); mkdirErr == nil {
				if writeErr := os.WriteFile(filepath.Join(dir, "verification-telemetry.jsonl"), data, 0o600); writeErr != nil {
					fmt.Fprintln(os.Stderr, "retain verification telemetry:", writeErr)
				}
			} else {
				fmt.Fprintln(os.Stderr, "retain verification telemetry:", mkdirErr)
			}
		}
		_ = os.RemoveAll(home)
	}, nil
}

func verificationIsolationEvidence(repoRoot string, artifacts harnessArtifactContext) harnessStep {
	started := time.Now()
	step := harnessStep{Name: "verification isolation", Command: []string{"go", "run", "./scripts/verify", "--summary", "--write"}}
	evidence := newHarnessEvidence(step.Command, repoRoot, started)
	step.Evidence = &evidence
	home := envpolicy.Get("SCENERY_AGENT_HOME")
	private := strings.HasPrefix(home, filepath.Join(repoRoot, ".scenery", "harness", "verification-agent-"))
	data, err := os.ReadFile(filepath.Join(home, "telemetry.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		step.Error = err.Error()
		return step
	}
	purpose := envpolicy.Get("SCENERY_EXECUTION_PURPOSE")
	count := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record cliTelemetryRecord
		if err := json.Unmarshal(line, &record); err != nil || record.Purpose != purpose || record.InvocationID == "" || record.Producer == nil || record.Version != record.Producer.Version {
			step.Error = "verification descendant telemetry lacks the explicit lane purpose or resolved producer identity"
			return step
		}
		count++
	}
	if !private || envpolicy.Get("SCENERY_AGENT_SOCKET") != "" || envpolicy.Get("SCENERY_AGENT_ROUTER_ADDR") != "" {
		step.Error = "verification lane retained personal agent routing or home"
		return step
	}
	step.Evidence.Artifacts, step.Diagnostics = writeHarnessOutputEvidenceArtifacts(artifacts, step.Name, "verification-telemetry.snapshot.jsonl", "", data, nil)
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Evidence.ExitCode = intPtr(0)
	step.Summary = map[string]any{"private_fallback_home": true, "purpose": purpose, "descendant_cli_records": count, "personal_agent_routing": false}
	return step
}
