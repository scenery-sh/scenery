package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/toolchain"
)

const harnessToolchainSourceBuildProbeName = "toolchain source-build probe"

type harnessToolchainSourceBuildCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessToolchainSourceBuildProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessToolchainSourceBuildProbeStepWithCheck(ctx, repoRoot, runHarnessToolchainSourceBuildProbeCheck)
}

func runHarnessToolchainSourceBuildProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessToolchainSourceBuildCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessToolchainSourceBuildProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
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
				SuggestedAction: "Fix source-built toolchain installation, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessToolchainSourceBuildProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-toolchain-source-build-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()

	sourceRoot := filepath.Join(root, "source")
	if err := writeHarnessToolchainSourceFile(filepath.Join(sourceRoot, "go.mod"), "module example.test/sourcebuild\n\ngo 1.27.0\n", 0o644); err != nil {
		return nil, nil, err
	}
	if err := writeHarnessToolchainSourceFile(filepath.Join(sourceRoot, "cmd", "demo", "main.go"), "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"source-build-ok\") }\n", 0o644); err != nil {
		return nil, nil, err
	}
	manifest := toolchain.Manifest{
		Kind:           toolchain.ManifestKind,
		SchemaRevision: toolchain.ManifestSchemaRevision,
		Artifacts: []toolchain.Artifact{{
			Name: "demo-source", Kind: "binary", Version: "dev", DefaultBinary: "demo-source",
			SourceBuild: &toolchain.SourceBuildArtifact{Kind: "go", Package: "./cmd/demo"},
		}},
	}
	store, err := toolchain.NewStore(filepath.Join(root, "store"), manifest)
	if err != nil {
		return nil, nil, err
	}
	platform := toolchain.CurrentPlatform()
	status, err := store.Sync(ctx, toolchain.Options{RootDir: sourceRoot, Platform: platform, Tool: "demo-source"})
	if err != nil {
		return nil, nil, err
	}
	if len(status.Artifacts) != 1 || status.Artifacts[0].Status != "installed" || status.Artifacts[0].Source != "source-build" {
		return nil, nil, fmt.Errorf("source-build sync status = %+v", status.Artifacts)
	}
	installed := status.Artifacts[0].ManagedPath
	verified, err := store.Verify(ctx, toolchain.Options{RootDir: sourceRoot, Platform: platform, Tool: "demo-source"})
	if err != nil {
		return nil, nil, err
	}
	if len(verified.Artifacts) != 1 || verified.Artifacts[0].Status != "installed" {
		return nil, nil, fmt.Errorf("source-build verify status = %+v", verified.Artifacts)
	}
	command := exec.CommandContext(ctx, installed)
	command.Env = envpolicy.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("execute source-built toolchain artifact: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if got := strings.TrimSpace(string(output)); got != "source-build-ok" {
		return nil, nil, fmt.Errorf("source-built toolchain output = %q", got)
	}
	return map[string]any{
		"proof":        "source_built_toolchain_artifact_compiled_verified_and_executed",
		"platform":     platform.String(),
		"managed_path": installed,
		"output":       strings.TrimSpace(string(output)),
	}, nil, nil
}

func writeHarnessToolchainSourceFile(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), mode)
}
