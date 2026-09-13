package main

import (
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
)

func wireBuildGenerateHooks() {
	build.SetGenerateHooks(build.GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		SyncCachedTypeScript: func(result *compiler.Result) ([]string, error) {
			generated, err := generate.SyncCachedTypeScriptClients(result)
			return generated.Checked, err
		},
		PrepareBuildGoWorkspace: generate.PrepareBuildGoWorkspace,
		RuntimeIntegrationPlan:  generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:   generate.RenderAssistantAssetRegistry,
	})
}
