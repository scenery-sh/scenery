package main

import (
	"bytes"
	"context"
	"strings"
	"time"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func runHarnessFixtureCheckWithRunner(ctx context.Context, repoRoot, appRoot string, run productCommandRunner) harnessStep {
	started := time.Now()
	args := []string{"check", "--app-root", appRoot, "-o", "json"}
	var output bytes.Buffer
	err := run(ctx, repoRoot, &output, args...)
	step := harnessStep{Name: "check", Command: append([]string{harnessLocalSceneryBinaryPath(repoRoot)}, args...), DurationMS: time.Since(started).Milliseconds()}
	envelope, decodeErr := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
	step.OK = err == nil && decodeErr == nil && envelope.OK
	if decodeErr == nil {
		step.Summary = map[string]any{"diagnostics": len(envelope.Diagnostics)}
		for _, diagnostic := range envelope.Diagnostics {
			step.Diagnostics = append(step.Diagnostics, checkDiagnostic{Stage: "check", Severity: diagnostic.Severity, File: diagnostic.Path, Message: diagnostic.Message, SuggestedAction: strings.Join(diagnostic.Suggestions, "; ")})
		}
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
	} else if decodeErr != nil {
		step.Error = "invalid check JSON: " + decodeErr.Error()
	}
	return step
}

func summarizeHarnessFixtureInspect(subject string, payload map[string]any) map[string]any {
	summary := map[string]any{}
	for _, key := range []string{"kind", "schema_revision"} {
		if value, ok := payload[key].(string); ok && value != "" {
			summary[key] = value
		}
	}
	if subject == "app" {
		if counts, ok := payload["counts"].(map[string]any); ok {
			for key, value := range counts {
				summary[key] = value
			}
		}
	} else if items, ok := payload[subject].([]any); ok {
		summary[subject] = len(items)
	}
	return summary
}
