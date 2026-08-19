package main

import (
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
)

func wireBuildGenerateHooks() {
	build.SetGenerateHooks(build.GenerateHooks{
		ApplyImplementationCheck: generate.ApplyImplementationCheck,
		SyncEditorWorkspace:      generate.SyncEditorWorkspace,
		SyncCachedTypeScript: func(result *compiler.Result) error {
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
