package main

import (
	"context"
	"path/filepath"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/toolchain"
)

func wireBuildGenerateHooks() {
	generate.SetImplementationProofProducer(build.LinkedFrameworkDigest())
	if root := sharedCacheRoot(); root != "" {
		build.SetSharedDevelopmentProcessStore(filepath.Join(root, "process-executables"))
		toolchain.SetSharedArtifactStore(filepath.Join(root, "toolchain-artifacts"))
	}
	build.SetGenerateHooks(build.GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		ApplyVerifiedImplementationCheck: generate.ApplyVerifiedImplementationCheck,
		SyncCachedTypeScript: func(ctx context.Context, result *compiler.Result) ([]string, error) {
			generated, err := generate.SyncCachedTypeScriptClientsContext(ctx, result)
			return generated.Checked, err
		},
		PrepareBuildGoWorkspace: generate.PrepareBuildGoWorkspaceContext,
		RuntimeIntegrationPlan:  generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:   generate.RenderAssistantAssetRegistry,
	})
}
