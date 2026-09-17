package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/envpolicy"
)

const harnessAssistantHelperProbeName = "assistant helper protocol probe"

// The assistant-helper probe renders the generated Eve channel and connection
// and runs their run attribution, event ownership and approval protocol under
// Node against a simulated Eve runtime
// (internal/assistantadapter/eve/testdata/helper-protocol.test.mjs).
func runHarnessAssistantHelperProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessAssistantHelperProbeName, Command: []string{"go", "run", "./scripts/verify", "--probe", "assistant-helper", "--summary"}}
	summary, err := runHarnessAssistantHelperProbe(ctx, repoRoot)
	step.Summary, step.DurationMS = summary, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error,
			SuggestedAction: "Fix the generated Eve helper's run attribution or event ownership, then rerun `go run ./scripts/verify --probe assistant-helper --summary --write`."}}
		return step
	}
	step.OK = true
	return step
}

func runHarnessAssistantHelperProbe(parent context.Context, repoRoot string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("the assistant helper protocol proof needs node on PATH: %w", err)
	}
	root, err := os.MkdirTemp("", "scenery-assistant-helper-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	overlay := filepath.Join(root, "overlay")
	if _, err := eve.MaterializeOverlay(eve.OverlayRequest{
		SourceRoot: filepath.Join(repoRoot, "internal", "assistantadapter", "eve", "testdata", "project"), OverlayRoot: overlay,
		AssistantAddress: "app/assistant/support", RuntimeRevision: "sha256:runtime-proof", CapabilityRevision: "sha256:capability-proof",
		ApprovalNeverTools: []string{"scenery__safe"}, ControlURL: "http://127.0.0.1:4454", MCPURL: "http://127.0.0.1:4455",
	}); err != nil {
		return nil, err
	}
	// The proof replaces the Eve package with the three definition functions
	// the generated files import; everything else is the simulated runtime.
	for path, content := range map[string]string{
		"node_modules/eve/package.json":   `{"name":"eve","type":"module","exports":{"./channels":"./channels.js","./connections":"./connections.js"}}`,
		"node_modules/eve/channels.js":    "export const defineChannel = (definition) => definition;\nexport const GET = (path, handler) => ({ method: \"GET\", path, handler });\nexport const POST = (path, handler) => ({ method: \"POST\", path, handler });\n",
		"node_modules/eve/connections.js": "export const defineMcpClientConnection = (definition) => definition;\n",
		"agent/channels/scenery.js":       "export * from \"./scenery.ts\";\nexport { default } from \"./scenery.ts\";\n",
	} {
		target := filepath.Join(overlay, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	proof := filepath.Join(repoRoot, "internal", "assistantadapter", "eve", "testdata", "helper-protocol.test.mjs")
	command := exec.CommandContext(ctx, node, "--test", "--test-timeout=20000", "--test-reporter=tap", proof)
	command.Dir = overlay
	command.Env = append(envpolicy.Environ(), "NODE_NO_WARNINGS=1")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("helper protocol proof failed: %w\n%s", err, harnessTail(output.String(), 60))
	}
	passed := strings.Count(output.String(), "\nok ")
	if passed == 0 || strings.Contains(output.String(), "\nnot ok ") {
		return nil, fmt.Errorf("helper protocol proof reported no passing tests:\n%s", harnessTail(output.String(), 60))
	}
	return map[string]any{"node": node, "passed_tests": passed, "proof": "generated_eve_helper_attributes_tool_calls_and_events_to_the_run_of_the_executing_turn"}, nil
}

func harnessTail(text string, lines int) string {
	parts := strings.Split(strings.TrimSpace(text), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}
