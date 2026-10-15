package main

import (
	"context"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
)

func wireBuildGenerateHooks() {
	build.SetGenerateHooks(build.GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		SyncCachedTypeScript: func(ctx context.Context, result *compiler.Result) ([]string, error) {
			generated, err := generate.SyncCachedTypeScriptClientsContext(ctx, result)
			return generated.Checked, err
		},
		PrepareBuildGoWorkspace: generate.PrepareBuildGoWorkspaceContext,
		RuntimeIntegrationPlan:  generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:   generate.RenderAssistantAssetRegistry,
	})
}
