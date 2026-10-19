package graph

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ListOptions selects one page of resources.list. Kind and Module filter the
// view's resources; Limit bounds the page; Cursor continues an earlier page of
// the same query and snapshot; Summary returns only resource identities.
type ListOptions struct {
	Kind    string `json:"kind,omitempty"`
	Module  string `json:"module,omitempty"`
	View    string `json:"view,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Summary bool   `json:"summary,omitempty"`
}

// ResourceSummary is the compact identity of a listed resource.
type ResourceSummary struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Module  string `json:"module"`
}

// ResourceList is one page of resources.list. NextCursor is present exactly
// when Truncated is true.
type ResourceList struct {
	View             string `json:"view"`
	ContractRevision string `json:"contract_revision"`
	Resources        any    `json:"resources"`
	Truncated        bool   `json:"truncated"`
	NextCursor       string `json:"next_cursor,omitempty"`
}

// ListAt returns one deterministic page of the manifest's matching resources.
// A cursor is bound to the workspace and contract revisions and to the exact
// query, and expires with the context-continuation lifetime.
func ListAt(manifest *Manifest, workspaceRevision string, options ListOptions, now time.Time) (ResourceList, error) {
	if manifest == nil {
		return ResourceList{}, fmt.Errorf("failed_precondition: no valid manifest is available")
	}
	if options.Limit == 0 {
		options.Limit = AgentMaxResources
	}
	if options.Limit < 0 || options.Limit > AgentMaxResources {
		return ResourceList{}, fmt.Errorf("invalid_request: limit must be between 1 and %d", AgentMaxResources)
	}
	queryDigest := listQueryDigest(options)
	offset := 0
	if options.Cursor != "" {
		payload, err := ParseContextToken(options.Cursor)
		if err != nil {
			return ResourceList{}, fmt.Errorf("failed_precondition: invalid cursor")
		}
		if payload.WorkspaceRevision != workspaceRevision || payload.ContractRevision != manifest.ContractRevision {
			return ResourceList{}, fmt.Errorf("failed_precondition: cursor snapshot is unavailable")
		}
		if payload.QueryDigest != queryDigest {
			return ResourceList{}, fmt.Errorf("failed_precondition: cursor does not match the query")
		}
		if !now.Before(time.Unix(payload.ExpiresUnix, 0)) {
			return ResourceList{}, fmt.Errorf("failed_precondition: cursor expired")
		}
		offset = payload.Offset
	}
	matching := make([]Resource, 0)
	for _, resource := range manifest.Resources {
		if (options.Kind == "" || listKindMatches(resource, options.Kind)) && (options.Module == "" || resource.Module == options.Module) {
			matching = append(matching, resource)
		}
	}
	if offset > len(matching) {
		return ResourceList{}, fmt.Errorf("failed_precondition: cursor offset is unavailable")
	}
	end := min(offset+options.Limit, len(matching))
	page := matching[offset:end]
	list := ResourceList{View: options.View, ContractRevision: manifest.ContractRevision, Resources: page, Truncated: end < len(matching)}
	if options.Summary {
		summaries := make([]ResourceSummary, 0, len(page))
		for _, resource := range page {
			summaries = append(summaries, ResourceSummary{Address: resource.Address, Kind: resource.Kind, Name: resource.Name, Module: resource.Module})
		}
		list.Resources = summaries
	}
	if list.Truncated {
		list.NextCursor = makeContextToken(ContextToken{
			WorkspaceRevision: workspaceRevision, ContractRevision: manifest.ContractRevision,
			QueryDigest: queryDigest, Offset: end, ExpiresUnix: now.Add(ContextTokenTTL).Unix(),
		})
	}
	return list, nil
}

// listKindMatches accepts a full kind or its short spelling (record,
// http_gateway).
func listKindMatches(resource Resource, value string) bool {
	return resource.Kind == value || strings.TrimPrefix(resource.Kind, "scenery.") == strings.ReplaceAll(value, "_", "-")
}

func listQueryDigest(options ListOptions) string {
	options.Cursor = ""
	encoded, _ := json.Marshal(options)
	sum := prefixedSHA256("scenery.resources-list-query\x00", encoded)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
