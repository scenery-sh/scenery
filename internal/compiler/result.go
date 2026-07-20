// Package compiler owns source loading, validation, and immutable graph results.
package compiler

import (
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

type Result struct {
	Root                    string                     `json:"-"`
	Manifest                *graph.Manifest            `json:"manifest,omitempty"`
	FrameworkResources      []graph.Resource           `json:"-"`
	ViewManifests           map[string]*graph.Manifest `json:"-"`
	PartialGraph            *graph.PartialGraph        `json:"partial_graph,omitempty"`
	ContractStatus          string                     `json:"contract_status"`
	ImplementationStatus    string                     `json:"implementation_status"`
	WorkspaceRevision       string                     `json:"workspace_revision"`
	ImplementationRevisions map[string]string          `json:"implementation_revision,omitempty"`
	DeploymentRevisions     map[string]string          `json:"deployment_revision,omitempty"`
	HTTPSurfaceRevisions    map[string]string          `json:"http_surface_revision,omitempty"`
	OpenAPIRevisions        map[string]string          `json:"openapi_revision,omitempty"`
	Diagnostics             []graph.Diagnostic         `json:"diagnostics"`
	Sources                 []*scn.Source              `json:"-"`
}

// ManifestForView returns one immutable compiler snapshot.
func (r *Result) ManifestForView(view string) (*graph.Manifest, error) {
	if r == nil {
		return graph.ManifestForView(nil, nil, view)
	}
	return graph.ManifestForView(r.Manifest, r.ViewManifests, view)
}

func (r *Result) Valid() bool {
	if r == nil || r.ContractStatus != "valid" || r.Manifest == nil {
		return false
	}
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == "error" {
			return false
		}
	}
	return true
}
