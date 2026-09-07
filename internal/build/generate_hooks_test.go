package build

import "scenery.sh/internal/generate"

func init() {
	SetGenerateHooks(GenerateHooks{
		ApplyImplementationCheck: generate.ApplyImplementationCheck,
		SyncGoPackages: func(result *generate.Result) error {
			_, err := generate.GenerateGoContractsFromResult(result, false)
			return err
		},
		SyncCachedTypeScript: func(result *generate.Result) error {
			_, err := generate.SyncCachedTypeScriptClients(result)
			return err
		},
		RenderGoWorkspaceFiles: generate.RenderGoWorkspaceFiles,
		GoVerificationOverlay:  generate.GoVerificationOverlay,
		GoVerificationPatterns: generate.GoVerificationPatterns,
		RuntimeIntegrationPlan: generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:  generate.RenderAssistantAssetRegistry,
	})
}
