package build

import (
	"path/filepath"
	"slices"

	"scenery.sh/internal/compiler"
)

// Startup discovers assistant watch inputs from a complete graph before build
// preparation. Reuse that graph only after the compiler verifies all current
// source membership/bytes and workspace identity; stale snapshots compile anew.
func compileWorkspaceContract(root string, snapshot *SourceSnapshot) (*compiler.Result, error) {
	if snapshot != nil && snapshot.Contract != nil && filepath.Clean(snapshot.Contract.Root) == filepath.Clean(root) {
		unchanged, err := compiler.SnapshotUnchanged(snapshot.Contract)
		if err != nil {
			return nil, err
		}
		if unchanged {
			// Implementation checking adds diagnostics/status to the build's
			// result. Do not mutate the source snapshot's diagnostic ownership.
			result := *snapshot.Contract
			manifest := *result.Manifest
			result.Manifest = &manifest
			result.Diagnostics = slices.Clone(result.Diagnostics)
			manifest.Diagnostics = slices.Clone(manifest.Diagnostics)
			return &result, nil
		}
	}
	return compiler.Check(root)
}
