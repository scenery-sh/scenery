package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"scenery.sh/internal/assistantadapter/eve"
)

const harnessAssistantHelperProbeName = "assistant helper protocol probe"

// The assistant-helper probe renders the generated Eve channel and connection
// and runs their run attribution, event ownership and approval protocol under
// Bun against a simulated Eve runtime
// (internal/assistantadapter/eve/testdata/helper-protocol.test.mjs).
func runHarnessAssistantHelperProbe(ctx context.Context, repoRoot string, resp *harnessSelfResponse, artifacts harnessArtifactContext) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	started := time.Now()
	prepare := harnessStep{Name: "assistant helper overlay preparation"}
	root, err := os.MkdirTemp("", "scenery-assistant-helper-")
	if err != nil {
		prepare.Error = err.Error()
		resp.Steps = append(resp.Steps, prepare)
		return
	}
	defer func() { _ = os.RemoveAll(root) }()
	overlay := filepath.Join(root, "overlay")
	err = prepareHarnessAssistantHelperOverlay(repoRoot, overlay)
	prepare.DurationMS = time.Since(started).Milliseconds()
	prepare.OK = err == nil
	prepare.Summary = map[string]any{"measurement_boundary": "overlay materialization and simulated runtime preparation"}
	if err != nil {
		prepare.Error = err.Error()
	}
	resp.Steps = append(resp.Steps, prepare)
	if err != nil {
		return
	}
	files := []string{"internal/assistantadapter/eve/testdata/helper-protocol.test.mjs"}
	expected := 11
	step := runHarnessJUnitStep(ctx, overlay, harnessAssistantHelperProbeName, "bun", files, &expected, artifacts, func(report string) []string {
		// The generated modules share state; keep the protocol cases serial.
		return []string{"bun", "test", "--timeout=20000", "--max-concurrency=1", "--reporter=junit", "--reporter-outfile=" + report,
			filepath.Join(repoRoot, filepath.FromSlash(files[0]))}
	})
	step.Summary["proof"] = "generated_eve_helper_attributes_tool_calls_and_events_to_the_run_of_the_executing_turn"
	resp.Steps = append(resp.Steps, step)
}

func prepareHarnessAssistantHelperOverlay(repoRoot, overlay string) error {
	if _, err := eve.MaterializeOverlay(eve.OverlayRequest{
		SourceRoot: filepath.Join(repoRoot, "internal", "assistantadapter", "eve", "testdata", "project"), OverlayRoot: overlay,
		AssistantAddress: "app/assistant/support", RuntimeRevision: "sha256:runtime-proof", CapabilityRevision: "sha256:capability-proof",
		ApprovalNeverTools: []string{"scenery__safe"}, ControlURL: "http://127.0.0.1:4454", MCPURL: "http://127.0.0.1:4455",
	}); err != nil {
		return err
	}
	// The proof replaces the Eve package with the three definition functions
	// the generated files import; everything else is the simulated runtime.
	for path, content := range map[string]string{
		"node_modules/eve/package.json": `{"name":"eve","type":"module","exports":{"./channels":"./channels.js","./connections":"./connections.js"}}`,
		"node_modules/eve/channels.js":  "export const defineChannel = (definition) => definition;\nexport const GET = (path, handler) => ({ method: \"GET\", path, handler });\nexport const POST = (path, handler) => ({ method: \"POST\", path, handler });\n",
		// defineDynamic resolves the connection at a session boundary, which is
		// how the generated connection reaches the gateway address supervision
		// supplies; the simulation records the definition it returns.
		"node_modules/eve/connections.js": "export const defineMcpClientConnection = (definition) => definition;\nexport const defineDynamic = (definition) => definition;\n",
		"agent/channels/scenery.js":       "export * from \"./scenery.ts\";\nexport { default } from \"./scenery.ts\";\n",
	} {
		target := filepath.Join(overlay, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}
