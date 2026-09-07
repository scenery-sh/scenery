package generate

import (
	"scenery.sh/internal/compiler"
)

type ClientCoverage struct {
	Target   string `json:"target"`
	Bindings int    `json:"bindings"`
	Message  string `json:"message,omitempty"`
}

// ClientCoverageFor describes the same selection used to render fetch clients.
func ClientCoverageFor(result *compiler.Result, selector string) []ClientCoverage {
	coverage := []ClientCoverage{}
	if result == nil || result.Manifest == nil {
		return coverage
	}
	resources := append(append([]Resource(nil), result.Manifest.Resources...), result.FrameworkResources...)
	for _, target := range typescriptTargets(result.Manifest.Resources, selector) {
		item := ClientCoverage{Target: target.Address, Bindings: len(publicHTTPBindings(resources, target))}
		if item.Bindings == 0 && len(reachableAssistantSurfaces(resources, target)) == 0 {
			item.Message = "no exported HTTP bindings selected; check package operation exports and the target's gateways/include selectors"
		}
		coverage = append(coverage, item)
	}
	return coverage
}
