package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const harnessAssistantProductionProbeName = "assistant production process probe"

type harnessAssistantProductionCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessAssistantProductionProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessAssistantProductionProbeStepWithCheck(ctx, repoRoot, runHarnessAssistantProductionProbeCheck)
}

func runHarnessAssistantProductionProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessAssistantProductionCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessAssistantProductionProbeName,
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the production assistant asset/process lifecycle, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

// runHarnessAssistantProductionProbeCheck runs the application host's
// assistant lifecycle: a real helper process behind the public conversation
// route and the MCP endpoint, served from a production bundle plan.
func runHarnessAssistantProductionProbeCheck(parent context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	manifest := filepath.Join(repoRoot, "sdk", "rust", "scenery-kernel", "Cargo.toml")
	command := exec.CommandContext(ctx, "cargo", "+"+harnessRustToolchain, "test", "--locked", "--manifest-path", manifest, "assistant_test::")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("application host assistant lifecycle: %w\n%s", err, output)
	}
	if !strings.Contains(string(output), "test result: ok. 1 passed") {
		return nil, nil, fmt.Errorf("application host assistant lifecycle ran no test:\n%s", output)
	}
	return map[string]any{"proof": "host_assistant_gateway_and_mcp_with_a_real_helper_process", "test": "assistant_test::assistant_and_mcp_use_the_public_and_worker_protocols"}, nil, nil
}

// harnessRustToolchain is the pinned toolchain of the application host crate.
const harnessRustToolchain = "1.99.1"
