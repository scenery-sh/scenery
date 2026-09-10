package build

import "scenery.sh/internal/compiler"

// Restore the identity needed by candidate preflight after the source and
// executable fingerprints have established a cache hit. The executable's own
// handshake still has to match this descriptor before it may start workers.
func restoreCachedRuntimeIdentity(result *Result) bool {
	if result == nil || result.Contract == nil || result.Contract.Manifest == nil {
		return false
	}
	target, err := compiler.ResolveGoBuildTarget(result.Contract, "", "development")
	if err != nil {
		return false
	}
	bundle, err := ReadRuntimeBundle(result.AppRoot, target.Name)
	if err != nil || bundle.RuntimeABI != "scenery.go-runtime/v1" ||
		bundle.Application != result.Contract.Manifest.Application.Name ||
		bundle.ContractRevision != result.Contract.Manifest.ContractRevision {
		return false
	}
	revisions, diagnostics := compiler.ComputeImplementationRevisions(result.Contract, map[string]string{target.Name: bundle.BuildInput.Digest})
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return false
		}
	}
	if revisions[target.Name] != bundle.ImplementationRevision {
		return false
	}
	result.Target = &target
	result.BuildInput = bundle.BuildInput
	result.ImplementationRevisions = revisions
	result.AssistantAssets = bundle.AssistantAssets
	return true
}
