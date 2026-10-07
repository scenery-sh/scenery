package main

import (
	"context"
	"path/filepath"
	"strings"
)

// One inventory owns explicit external proof and the functional release set.
type harnessProbe struct {
	id  string
	run func(context.Context, string, *harnessSelfResponse, harnessArtifactContext)
}

func runHarnessProbe(ctx context.Context, root string, resp *harnessSelfResponse, artifacts harnessArtifactContext, probe harnessProbe) {
	first := len(resp.Steps)
	probe.run(ctx, root, resp, artifacts)
	command := []string{"go", "run", "./scripts/verify", "--repo-root", root, "--probe", probe.id, "--summary", "--write"}
	for i := first; i < len(resp.Steps); i++ {
		step := &resp.Steps[i]
		step.Command = append([]string(nil), command...)
		if step.Evidence != nil {
			// Preserve the actual subprocess argv/cwd while making its rerun
			// rebuild prerequisites and select only this probe.
			step.Evidence.ReproCommand = reproCommand(command, root)
		}
		for j := range step.Diagnostics {
			action := step.Diagnostics[j].SuggestedAction
			start := strings.Index(action, "`go run ./scripts/verify")
			if start < 0 {
				continue
			}
			end := strings.Index(action[start+1:], "`")
			if end >= 0 {
				step.Diagnostics[j].SuggestedAction = action[:start+1] + reproCommand(command, "") + action[start+1+end:]
			}
		}
	}
}

func harnessSingleProbe(id string, run func(context.Context, string) harnessStep) harnessProbe {
	return harnessProbe{id: id, run: func(ctx context.Context, root string, resp *harnessSelfResponse, _ harnessArtifactContext) {
		resp.Steps = append(resp.Steps, run(ctx, root))
	}}
}

func harnessProbeCatalog() []harnessProbe {
	return []harnessProbe{
		harnessSingleProbe("parallel-runtime", runHarnessParallelDevStep),
		harnessSingleProbe("postgres", func(ctx context.Context, root string) harnessStep {
			return runHarnessPostgresProbeStep(ctx, root, true)
		}),
		{id: "ui", run: runHarnessUIProbe},
		{id: "frontend", run: func(ctx context.Context, root string, resp *harnessSelfResponse, artifacts harnessArtifactContext) {
			resp.Steps = append(resp.Steps, runHarnessExecStep(ctx, root, "frontend readiness and production rebuild probe", []string{"go", "test", "-tags=scenery_frontend_integration", "./cmd/scenery", "-run=^TestFrontendReadinessIntegration$", "-v", "-count=1"}, artifacts))
		}},
		{id: "fixtures", run: func(ctx context.Context, root string, resp *harnessSelfResponse, _ harnessArtifactContext) {
			step, matrix := runHarnessFixtureMatrixStep(ctx, root)
			resp.FixtureMatrix = matrix
			resp.Steps = append(resp.Steps, step)
		}},
		harnessSingleProbe("storage", func(ctx context.Context, root string) harnessStep {
			return runHarnessStorageProbeStep(ctx, root, harnessLocalSceneryBinaryPath(root))
		}),
		harnessSingleProbe("core-separation", runHarnessCoreSeparationStep),
		harnessSingleProbe("capability-authority", runHarnessCapabilityAuthorityStep),
		harnessSingleProbe("auth", runHarnessStandardAuthStep),
		harnessSingleProbe("worktree", runHarnessWorktreeRuntimeProbeStep),
		harnessSingleProbe("agent-restart", runHarnessAgentRestartProbeStep),
		harnessSingleProbe("assistant-init", runHarnessAssistantInitProbeStep),
		harnessSingleProbe("assistant-runtime", runHarnessAssistantProductionProbeStep),
		{id: "assistant-helper", run: runHarnessAssistantHelperProbe},
		harnessSingleProbe("assistant-journey", runHarnessAssistantJourneyProbeStep),
		harnessSingleProbe("build-info", runHarnessBuildInfoProbeStep),
		harnessSingleProbe("cli-process", runHarnessCLIProcessProbeStep),
		harnessSingleProbe("cli-grammar", runHarnessCLIGrammarProbeStep),
		harnessSingleProbe("dev-follower", runHarnessDevFollowProbeStep),
		harnessSingleProbe("dev-process", runHarnessDevManagedProcessProbeStep),
		harnessSingleProbe("process-model", runHarnessProcessModelProbeStep),
		harnessSingleProbe("dev-lock", runHarnessDevNamedLockProbeStep),
		harnessSingleProbe("dev-cleanup", runHarnessDevSessionCleanupProbeStep),
		harnessSingleProbe("inspect-go", runHarnessInspectDocsGoPackageProbeStep),
		harnessSingleProbe("toolchain-build", runHarnessToolchainSourceBuildProbeStep),
		harnessSingleProbe("worktree-git", runHarnessWorktreeGitProbeStep),
		harnessSingleProbe("edge", runHarnessEdgeProcessProbeStep),
		harnessSingleProbe("generation", runHarnessGenerationCompileProbeStep),
		{id: "native-contract", run: func(ctx context.Context, root string, resp *harnessSelfResponse, artifacts harnessArtifactContext) {
			resp.Steps = append(resp.Steps, runHarnessNativeContractApplicationProbeStepWithCheck(ctx, root, func(ctx context.Context, root string) (map[string]any, []checkDiagnostic, error) {
				return runHarnessNativeContractApplicationProbeCheckWithArtifacts(ctx, root, artifacts)
			}))
		}},
		harnessSingleProbe("snapshot-backup", runHarnessSnapshotBackupProbeStep),
		harnessSingleProbe("typescript", runHarnessTypeScriptCheckerProbeStep),
		harnessSingleProbe("code-task", runHarnessCodeTaskProcessProbeStep),
		{id: "observability", run: func(ctx context.Context, root string, resp *harnessSelfResponse, artifacts harnessArtifactContext) {
			resp.Steps = append(resp.Steps, runHarnessObservabilityProbeStepWithArtifacts(ctx, root, artifacts))
		}},
		harnessSingleProbe("victoria", runHarnessVictoriaProcessProbeStep),
		harnessSingleProbe("desktop", runHarnessDesktopProcessProbeStep),
		harnessSingleProbe("deploy-ssh", runHarnessDeploySSHProcessProbeStep),
		harnessSingleProbe("configuration", configurationProbeStep("environment configuration probe", runHarnessConfigurationProbe)),
		harnessSingleProbe("configuration-secrets", configurationProbeStep("environment configuration secrets probe", runHarnessConfigurationSecretsProbe)),
		harnessSingleProbe("configuration-deploy", configurationProbeStep("environment configuration deploy probe", runHarnessConfigurationDeployProbe)),
		{id: "validation-git", run: func(ctx context.Context, root string, resp *harnessSelfResponse, artifacts harnessArtifactContext) {
			resp.Steps = append(resp.Steps, runHarnessValidationGitProbeStepWithCheck(ctx, root, func(ctx context.Context, root string) (map[string]any, []checkDiagnostic, error) {
				return runHarnessValidationGitProbeCheckWithArtifacts(ctx, root, artifacts)
			}))
		}},
		{id: "test-cache", run: func(ctx context.Context, root string, resp *harnessSelfResponse, artifacts harnessArtifactContext) {
			resp.Steps = append(resp.Steps, runHarnessTestsuiteCacheProbeStepWithCheck(ctx, root, func(ctx context.Context, root string) (map[string]any, []checkDiagnostic, error) {
				return runHarnessTestsuiteCacheProbeCheckWithArtifacts(ctx, root, artifacts)
			}))
		}},
	}
}

func harnessProbeIDs() []string {
	var ids []string
	for _, probe := range harnessProbeCatalog() {
		ids = append(ids, probe.id)
	}
	return ids
}

func selectedHarnessProbes(opts harnessSelfOptions) []harnessProbe {
	var selected []harnessProbe
	for _, probe := range harnessProbeCatalog() {
		if opts.Mode == harnessSelfModeRelease {
			selected = append(selected, probe)
			continue
		}
		if opts.Mode == harnessSelfModeProbe {
			for _, id := range opts.Probes {
				if id == probe.id {
					selected = append(selected, probe)
					break
				}
			}
		}
	}
	return selected
}

func runHarnessUIProbe(ctx context.Context, repoRoot string, resp *harnessSelfResponse, artifactCtx harnessArtifactContext) {
	toolingRoot := filepath.Join(repoRoot, filepath.FromSlash(typescriptToolingRootRel))
	deps, ready := runHarnessTypeScriptDepsStep(ctx, toolingRoot, artifactCtx)
	resp.Steps = append(resp.Steps, deps)
	if !ready {
		return
	}
	tsc := filepath.Join(toolingRoot, "node_modules", ".bin", "tsc")
	resp.Steps = append(resp.Steps,
		runHarnessBunStep(ctx, repoRoot, "Scenery TypeScript client conformance", javascriptStageFiles("client"), nil, artifactCtx),
		runHarnessBunStep(ctx, repoRoot, "Scenery table behavior guards", javascriptStageFiles("table"), nil, artifactCtx),
		runHarnessBunStep(ctx, repoRoot, "Scenery runtime identity checks", javascriptStageFiles("identity"), nil, artifactCtx),
		runHarnessExecStep(ctx, repoRoot, "Scenery TypeScript client typecheck", []string{tsc, "--extendedDiagnostics", "-p", "internal/generate/testdata/tsconfig.generated-clients.json"}, artifactCtx),
		runHarnessExecStep(ctx, repoRoot, "Scenery UI catalog typecheck", []string{tsc, "--extendedDiagnostics", "-p", "internal/generate/testdata/tsconfig.catalog.json"}, artifactCtx),
	)
}
