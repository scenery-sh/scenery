package main

import (
	"context"
	"path/filepath"
)

// One inventory owns explicit external proof and the functional release set.
type harnessProbe struct {
	id  string
	run func(context.Context, string, *harnessSelfResponse, harnessArtifactContext)
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
		harnessSingleProbe("build-info", runHarnessBuildInfoProbeStep),
		harnessSingleProbe("cli-process", runHarnessCLIProcessProbeStep),
		harnessSingleProbe("dev-follower", runHarnessDevFollowProbeStep),
		harnessSingleProbe("dev-process", runHarnessDevManagedProcessProbeStep),
		harnessSingleProbe("dev-lock", runHarnessDevNamedLockProbeStep),
		harnessSingleProbe("dev-cleanup", runHarnessDevSessionCleanupProbeStep),
		harnessSingleProbe("inspect-go", runHarnessInspectDocsGoPackageProbeStep),
		harnessSingleProbe("toolchain-build", runHarnessToolchainSourceBuildProbeStep),
		harnessSingleProbe("worktree-git", runHarnessWorktreeGitProbeStep),
		harnessSingleProbe("edge", runHarnessEdgeProcessProbeStep),
		harnessSingleProbe("generation", runHarnessGenerationCompileProbeStep),
		harnessSingleProbe("native-contract", runHarnessNativeContractApplicationProbeStep),
		harnessSingleProbe("snapshot-backup", runHarnessSnapshotBackupProbeStep),
		harnessSingleProbe("typescript", runHarnessTypeScriptCheckerProbeStep),
		harnessSingleProbe("code-task", runHarnessCodeTaskProcessProbeStep),
		harnessSingleProbe("victoria", runHarnessVictoriaProcessProbeStep),
		harnessSingleProbe("desktop", runHarnessDesktopProcessProbeStep),
		harnessSingleProbe("deploy-ssh", runHarnessDeploySSHProcessProbeStep),
		harnessSingleProbe("validation-git", runHarnessValidationGitProbeStep),
		harnessSingleProbe("test-cache", runHarnessTestsuiteCacheProbeStep),
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
	dashboardUIRoot := filepath.Join(repoRoot, filepath.FromSlash(dashboardUIRootRel))
	deps, ready := runHarnessConsoleDepsStep(ctx, dashboardUIRoot, artifactCtx)
	resp.Steps = append(resp.Steps, deps)
	if !ready {
		return
	}
	resp.Steps = append(resp.Steps,
		runHarnessExecStep(ctx, dashboardUIRoot, "dashboard ui typecheck", []string{"bun", "run", "typecheck"}, artifactCtx),
		runHarnessExecStep(ctx, dashboardUIRoot, "dashboard ui build", []string{"bun", "run", "build"}, artifactCtx),
		runHarnessDashboardFreshnessStep(ctx, repoRoot),
		runHarnessExecStep(ctx, repoRoot, "Scenery TypeScript client conformance", []string{"bun", "test", "internal/generate/testdata/typescript_client_conformance.test.ts"}, artifactCtx),
		runHarnessExecStep(ctx, repoRoot, "Scenery TypeScript client typecheck", []string{filepath.Join(dashboardUIRoot, "node_modules", ".bin", "tsc"), "-p", "internal/generate/testdata/tsconfig.generated-clients.json"}, artifactCtx),
		runHarnessExecStep(ctx, repoRoot, "Scenery UI catalog typecheck", []string{filepath.Join(dashboardUIRoot, "node_modules", ".bin", "tsc"), "-p", "internal/generate/testdata/tsconfig.catalog.json"}, artifactCtx),
	)
}
