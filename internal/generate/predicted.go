package generate

import (
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
)

// CheckPredictedGoContracts renders Go contracts from one compiler snapshot
// without writing the workspace. Evolution planning injects this check.
func CheckPredictedGoContracts(result *compiler.Result) error {
	_, err := RenderGoWorkspaceFiles(result)
	return err
}

// CheckPredictedTypeScriptClients renders TypeScript clients from one compiler
// snapshot without writing the workspace. Evolution planning injects this check.
func CheckPredictedTypeScriptClients(result *compiler.Result) error {
	_, err := selectedTypeScriptRenderer("")(result)
	return err
}

// ApplyImplementationCheck records generation and native-implementation
// diagnostics on the compiler snapshot used to perform the check.
func ApplyImplementationCheck(result *compiler.Result) {
	applyImplementationCheck(result, Check)
}

// ApplyImplementationCheckWithAnalysis performs the complete check and exposes
// successful target analyses for reuse within the same build preparation only.
// Consumers must match the complete target context, including overlay patterns.
func ApplyImplementationCheckWithAnalysis(result *compiler.Result, analyzed func(gotarget.Context, *model.App)) *generateapi.GoWorkspaceProjection {
	checked := checkWithGoAnalysis(result, analyzed)
	ApplyCheck(result, checked)
	return checked.goWorkspace
}

func applyImplementationCheck(result *compiler.Result, check func(*compiler.Result) CheckResult) {
	ApplyCheck(result, check(result))
}
