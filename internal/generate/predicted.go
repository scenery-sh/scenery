package generate

import "scenery.sh/internal/compiler"

// CheckPredictedGoContracts renders Go contracts from one compiler snapshot
// without writing the workspace. Evolution planning injects this check.
func CheckPredictedGoContracts(result *compiler.Result) error {
	_, err := GenerateGoContractsFromResult(result, false)
	return err
}

// CheckPredictedTypeScriptClients renders TypeScript clients from one compiler
// snapshot without writing the workspace. Evolution planning injects this check.
func CheckPredictedTypeScriptClients(result *compiler.Result) error {
	_, err := GenerateTypeScriptClientsFromResult(result, "", false)
	return err
}

// ApplyImplementationCheck records generation and native-implementation
// diagnostics on the compiler snapshot used to perform the check.
func ApplyImplementationCheck(result *compiler.Result) {
	ApplyCheck(result, Check(result))
}
