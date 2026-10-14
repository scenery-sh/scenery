package build

import (
	"context"
	"scenery.sh/internal/generate"
)

func init() {
	SetGenerateHooks(GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		SyncCachedTypeScript: func(ctx context.Context, result *generate.Result) ([]string, error) {
			generated, err := generate.SyncCachedTypeScriptClientsContext(ctx, result)
			return generated.Checked, err
		},
		PrepareBuildGoWorkspace: generate.PrepareBuildGoWorkspaceContext,
		RuntimeIntegrationPlan:  generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:   generate.RenderAssistantAssetRegistry,
	})
}
