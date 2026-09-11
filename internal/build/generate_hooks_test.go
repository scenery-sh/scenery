package build

import "scenery.sh/internal/generate"

func init() {
	SetGenerateHooks(GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		SyncCachedTypeScript: func(result *generate.Result) error {
			_, err := generate.SyncCachedTypeScriptClients(result)
			return err
		},
		PrepareBuildGoWorkspace: generate.PrepareBuildGoWorkspace,
		RuntimeIntegrationPlan:  generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:   generate.RenderAssistantAssetRegistry,
	})
}
