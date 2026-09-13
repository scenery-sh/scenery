package build

import (
	"context"
	"fmt"

	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// GenerateHooks are the generate callbacks used by prepare and assistant
// asset materialization. Production CLI and build tests wire them; the
// production package does not import internal/generate.
type GenerateHooks struct {
	ApplyPreparedImplementationCheck func(context.Context, *compiler.Result, string, []string, compiler.GoBuildTarget) error
	SyncCachedTypeScript             func(*compiler.Result) ([]string, error)
	PrepareBuildGoWorkspace          func(*compiler.Result) (generateapi.GoWorkspaceProjection, error)
	RuntimeIntegrationPlan           func(*compiler.Result) (generateapi.RuntimeIntegrationPlan, error)
	RenderAssistantAssets            func(*compiler.Result, []generateapi.AssistantAssetInput) (map[string][]byte, error)
}

var generateHooks GenerateHooks

// SetGenerateHooks installs the generate callbacks used by Prepare.
func SetGenerateHooks(hooks GenerateHooks) {
	generateHooks = hooks
}

func requireGenerateHooks() error {
	hooks := generateHooks
	if hooks.ApplyPreparedImplementationCheck == nil ||
		hooks.SyncCachedTypeScript == nil || hooks.PrepareBuildGoWorkspace == nil ||
		hooks.RuntimeIntegrationPlan == nil || hooks.RenderAssistantAssets == nil {
		return fmt.Errorf("build generate hooks are not wired")
	}
	return nil
}
