package main

import (
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
)

func wireBuildGenerateHooks() {
	build.SetGenerateHooks(build.GenerateHooks{
		ApplyImplementationCheck: generate.ApplyImplementationCheckWithAnalysis,
		SyncGoPackages: func(result *compiler.Result) error {
			_, err := generate.GenerateGoContractsFromResult(result, false)
			return err
		},
		SyncCachedTypeScript: func(result *compiler.Result) error {
			_, err := generate.SyncCachedTypeScriptClients(result)
			return err
		},
		RenderGoWorkspaceFiles: generate.RenderGoWorkspaceFiles,
		PrepareGoWorkspace:     generate.PrepareGoWorkspace,
		RuntimeIntegrationPlan: generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:  generate.RenderAssistantAssetRegistry,
	})
}
