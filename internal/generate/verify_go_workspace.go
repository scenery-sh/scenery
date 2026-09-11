package generate

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"scenery.sh/internal/compiler"
)

// ApplyPreparedImplementationCheck verifies fresh public-artifact ownership and
// every required native target against the private workspace consumed by build.
// The caller owns that workspace until this operation has returned.
func ApplyPreparedImplementationCheck(ctx context.Context, result *compiler.Result, workspace string, patterns []string, selected compiler.GoBuildTarget) error {
	if result == nil || !result.Valid() || workspace == "" {
		return fmt.Errorf("prepared implementation checking requires a valid graph and private workspace")
	}
	check := checkGeneratedArtifacts(result)
	check.ImplementationChecked = true
	var diagnostics []Diagnostic
	if err := validateInvariantPackageABIs(result); err != nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN6208", Severity: "error", Message: err.Error()})
	} else if targets, err := compiler.VerificationGoTargets(result); err != nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN6202", Severity: "error", Message: fmt.Sprintf("resolve Go verification targets: %v", err)})
	} else if targets, err = preparedGoVerificationTargets(result.Root, workspace, targets, selected); err != nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN6202", Severity: "error", Message: err.Error()})
	} else {
		diagnostics = verifyGoTargets(ctx, result, workspace, nil, patterns, targets)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	finishImplementationCheck(&check, diagnostics)
	ApplyCheck(result, check)
	return nil
}

func preparedGoVerificationTargets(root, workspace string, targets []compiler.GoBuildTarget, selected compiler.GoBuildTarget) ([]compiler.GoBuildTarget, error) {
	if selected.Context.ModuleRoot == "" || len(selected.Context.Patterns) == 0 {
		return nil, fmt.Errorf("prepared build target has no module or package patterns")
	}
	targets = slices.Clone(targets)
	matched := false
	for index, target := range targets {
		if reflect.DeepEqual(target.Context, selected.Context) {
			matched = true
			// Contract-only checking still checks bodies/types, but a runtime
			// build additionally needs service, handler and library ABI checks.
			if target.Role == "contract" {
				targets[index] = selected
			}
			break
		}
	}
	if !matched {
		targets = append(targets, selected)
	}
	for index := range targets {
		relative, err := filepath.Rel(root, targets[index].Context.ModuleRoot)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("go verification target %s is outside the prepared app workspace", targets[index].Address)
		}
		targets[index].Context.ModuleRoot = filepath.Join(workspace, relative)
	}
	return targets, nil
}
