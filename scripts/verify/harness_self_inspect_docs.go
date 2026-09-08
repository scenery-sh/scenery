package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

const harnessInspectDocsGoPackageProbeName = "inspect docs Go package probe"

type harnessInspectDocsGoPackageCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessInspectDocsGoPackageProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessInspectDocsGoPackageProbeStepWithCheck(ctx, repoRoot, runHarnessInspectDocsGoPackageProbeCheck)
}

func runHarnessInspectDocsGoPackageProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessInspectDocsGoPackageCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessInspectDocsGoPackageProbeName,
		Command: []string{"go", "list", "-find", "-f", "{{.ImportPath}}\\t{{.Dir}}", "./cmd/scenery"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix Go package ownership discovery, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessInspectDocsGoPackageProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	const targetPath = "cmd/scenery/inspect_docs.go"
	var output bytes.Buffer
	if err := runProduct(ctx, repoRoot, &output, "inspect", "docs", "--repo-root", repoRoot, "--for-path", targetPath, "-o", "json"); err != nil {
		return nil, nil, err
	}
	var response inspectDocsResponse
	if err := decodeCLIJSON(output.Bytes(), &response); err != nil {
		return nil, nil, err
	}
	const command = "go test ./cmd/scenery"
	count := 0
	for _, value := range response.VerificationCommands {
		if value == command {
			count++
		}
	}
	if count != 1 {
		return nil, nil, fmt.Errorf("public Go package ownership for %s = %+v, want exactly one %q", targetPath, response.VerificationCommands, command)
	}
	return map[string]any{
		"proof":                "public_inspect_docs_path_resolved_to_exact_go_package_verification",
		"target_path":          targetPath,
		"verification_command": command,
		"command_occurrences":  count,
	}, nil, nil
}
