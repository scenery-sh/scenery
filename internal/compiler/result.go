// Package compiler owns source loading, validation, and immutable graph results.
package compiler

import (
	"sort"
	"strings"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

type Result struct {
	Root                    string                     `json:"-"`
	Manifest                *graph.Manifest            `json:"manifest,omitempty"`
	FrameworkResources      []graph.Resource           `json:"-"`
	SQLRequirements         SQLRequirements            `json:"-"`
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

// AssistantRuntimeRevision is the helper implementation revision an assistant
// runs: the revision recorded for its own address, or the first recorded
// revision in address order when the compiler recorded none for it.
// Generation, the development runtime and the artifact build must answer this
// the same way, because the registered application and its prepared helper
// compare the revision on every control message.
func AssistantRuntimeRevision(revisions map[string]string, address string) string {
	if revision := strings.TrimSpace(revisions[address]); revision != "" {
		return revision
	}
	keys := make([]string, 0, len(revisions))
	for key := range revisions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if revision := strings.TrimSpace(revisions[key]); revision != "" {
			return revision
		}
	}
	return DefaultAssistantRuntimeRevision
}

// DefaultAssistantRuntimeRevision identifies a helper the compiler recorded no
// implementation revision for.
const DefaultAssistantRuntimeRevision = "runtime-1"

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
