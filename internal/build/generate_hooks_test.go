package build

import "scenery.sh/internal/generate"

func init() {
	SetGenerateHooks(GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		SyncCachedTypeScript: func(result *generate.Result) ([]string, error) {
			generated, err := generate.SyncCachedTypeScriptClients(result)
			return generated.Checked, err
		},
		PrepareBuildGoWorkspace: generate.PrepareBuildGoWorkspace,
		RuntimeIntegrationPlan:  generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:   generate.RenderAssistantAssetRegistry,
	})
}
