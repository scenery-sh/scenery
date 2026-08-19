package build

import (
	"fmt"

	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// GenerateHooks are the generate callbacks used by prepare and assistant
// asset materialization. Production CLI and build tests wire them; the
// production package does not import internal/generate.
type GenerateHooks struct {
	ApplyImplementationCheck func(*compiler.Result)
	SyncEditorWorkspace      func(*compiler.Result) error
	SyncCachedTypeScript     func(*compiler.Result) error
	RenderGoWorkspaceFiles   func(*compiler.Result) (map[string][]byte, error)
	GoVerificationOverlay    func(*compiler.Result) (map[string][]byte, error)
	GoVerificationPatterns   func(*compiler.Result) ([]string, error)
	RuntimeIntegrationPlan   func(*compiler.Result) (generateapi.RuntimeIntegrationPlan, error)
	RenderAssistantAssets    func(*compiler.Result, []generateapi.AssistantAssetInput) (map[string][]byte, error)
}

var generateHooks GenerateHooks

// SetGenerateHooks installs the generate callbacks used by Prepare.
func SetGenerateHooks(hooks GenerateHooks) {
	generateHooks = hooks
}

func requireGenerateHooks() error {
	hooks := generateHooks
	if hooks.ApplyImplementationCheck == nil || hooks.SyncEditorWorkspace == nil ||
		hooks.SyncCachedTypeScript == nil || hooks.RenderGoWorkspaceFiles == nil ||
		hooks.GoVerificationOverlay == nil || hooks.GoVerificationPatterns == nil ||
		hooks.RuntimeIntegrationPlan == nil || hooks.RenderAssistantAssets == nil {
		return fmt.Errorf("build generate hooks are not wired")
	}
	return nil
}
