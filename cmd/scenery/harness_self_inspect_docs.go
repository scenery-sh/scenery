package main

import (
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
				SuggestedAction: "Fix Go package ownership discovery, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessInspectDocsGoPackageProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	const targetPath = "cmd/scenery/inspect_docs.go"
	packages, err := inspectDocsGoPackagesForPath(ctx, repoRoot, targetPath)
	if err != nil {
		return nil, nil, err
	}
	if len(packages) != 1 {
		return nil, nil, fmt.Errorf("go package ownership for %s = %+v, want one package", targetPath, packages)
	}
	got := packages[0]
	if got.ImportPath != "scenery.sh/cmd/scenery" || got.RelDir != "cmd/scenery" {
		return nil, nil, fmt.Errorf("go package ownership for %s = %+v", targetPath, got)
	}
	return map[string]any{
		"proof":       "inspect_docs_path_resolved_by_real_go_package_loader",
		"target_path": targetPath,
		"import_path": got.ImportPath,
		"package_dir": got.RelDir,
	}, nil, nil
}
