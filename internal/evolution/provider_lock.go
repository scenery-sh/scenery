package evolution

import (
	"bytes"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/scn"
)

type ProviderLockResult struct {
	Path      string               `json:"path"`
	Changed   bool                 `json:"changed"`
	Checked   bool                 `json:"checked"`
	Providers []compiler.LockEntry `json:"providers"`
}

// SyncBuiltinProviderLocks uses the same revision-checked source transaction as
// semantic changes; it never writes another dependency or downloads a provider.
func SyncBuiltinProviderLocks(root string, check bool) (ProviderLockResult, error) {
	plan, err := compiler.PlanBuiltinProviderLocks(root)
	result := ProviderLockResult{Path: scn.AppLockFilename, Checked: check, Providers: plan.Providers}
	if err != nil {
		return result, err
	}
	result.Changed = !bytes.Equal(plan.Before, plan.After)
	if check || !result.Changed {
		return result, nil
	}
	_, finalize, err := commitPlannedEdits(root, []SourceEdit{{Path: scn.AppLockFilename, BeforeDigest: byteDigest(plan.Before), BeforeExists: plan.Exists, AfterExists: true, After: plan.After, Mode: plan.Mode}}, "")
	if err != nil {
		return result, err
	}
	finalize()
	return result, nil
}
