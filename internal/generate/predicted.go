package generate

import (
	"scenery.sh/internal/compiler"
)

// ApplyImplementationCheck records generation and native-implementation
// diagnostics on the compiler snapshot used to perform the check.
func ApplyImplementationCheck(result *compiler.Result) {
	applyImplementationCheck(result, Check)
}

func applyImplementationCheck(result *compiler.Result, check func(*compiler.Result) CheckResult) {
	ApplyCheck(result, check(result))
}
