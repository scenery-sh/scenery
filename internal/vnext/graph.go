package vnext

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const contextTokenTTL = 5 * time.Minute

const (
	agentMaxResources = 1000
	agentMaxBytes     = 2_000_000
	agentMaxDepth     = 16
)

type GraphOptions struct {
	Direction    string `json:"direction,omitempty"`
	Depth        int    `json:"depth,omitempty"`
	MaxResources int    `json:"max_resources,omitempty"`
}

type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path"`
}

type ResourceGraph struct {
	APIVersion string      `json:"api_version"`
	Focus      string      `json:"focus"`
	Direction  string      `json:"direction"`
	Resources  []Resource  `json:"resources"`
	Edges      []GraphEdge `json:"edges"`
	Truncated  bool        `json:"truncated"`
}

type ContextOptions struct {
	Focus             []string `json:"focus"`
	Include           []string `json:"include,omitempty"`
	Depth             int      `json:"depth,omitempty"`
	MaxResources      int      `json:"max_resources,omitempty"`
	MaxBytes          int      `json:"max_bytes,omitempty"`
	View              string   `json:"view,omitempty"`
	ContinuationToken string   `json:"continuation_token,omitempty"`
}

type ContextBundle struct {
	APIVersion        string                    `json:"api_version"`
	WorkspaceRevision string                    `json:"workspace_revision"`
	ContractRevision  string                    `json:"contract_revision"`
	View              string                    `json:"view"`
	Resources         []Resource                `json:"resources"`
	Schemas           map[string]map[string]any `json:"schemas,omitempty"`
	Diagnostics       []Diagnostic              `json:"diagnostics,omitempty"`
	Provenance        map[string]Origin         `json:"provenance,omitempty"`
	Truncated         bool                      `json:"truncated"`
	ContinuationToken string                    `json:"continuation_token,omitempty"`
}

func Graph(manifest *Manifest, focus string, options GraphOptions) (ResourceGraph, error) {
	return graph(manifest, focus, options, true)
}

func graph(manifest *Manifest, focus string, options GraphOptions, enforceTransportLimits bool) (ResourceGraph, error) {
	if manifest == nil {
		return ResourceGraph{}, fmt.Errorf("manifest is required")
	}
	if options.Direction == "" {
		options.Direction = "both"
	}
	if options.Depth < 0 {
		return ResourceGraph{}, fmt.Errorf("depth must be non-negative")
	}
	if options.Depth == 0 {
		options.Depth = 1
	}
	if enforceTransportLimits && options.Depth > agentMaxDepth {
		return ResourceGraph{}, fmt.Errorf("depth exceeds transport limit %d", agentMaxDepth)
	}
	if options.MaxResources <= 0 {
		options.MaxResources = 100
	}
	if enforceTransportLimits && options.MaxResources > agentMaxResources {
		return ResourceGraph{}, fmt.Errorf("max_resources exceeds transport limit %d", agentMaxResources)
	}
	if options.Direction != "dependencies" && options.Direction != "dependents" && options.Direction != "both" {
		return ResourceGraph{}, fmt.Errorf("direction must be dependencies, dependents, or both")
	}
	byAddress := resourcesByAddress(manifest)
	if _, ok := byAddress[focus]; !ok {
		return ResourceGraph{}, fmt.Errorf("resource %q not found", focus)
	}
	allEdges := resourceEdges(manifest.Resources)
	selected := map[string]bool{focus: true}
	frontier := []string{focus}
	for depth := 0; depth < options.Depth && len(frontier) > 0; depth++ {
		next := map[string]bool{}
		for _, address := range frontier {
			for _, edge := range allEdges {
				if (options.Direction == "dependencies" || options.Direction == "both") && edge.From == address && !selected[edge.To] {
					next[edge.To] = true
				}
				if (options.Direction == "dependents" || options.Direction == "both") && edge.To == address && !selected[edge.From] {
					next[edge.From] = true
				}
			}
		}
		frontier = frontier[:0]
		for address := range next {
			selected[address] = true
			frontier = append(frontier, address)
		}
		sort.Strings(frontier)
	}
	addresses := make([]string, 0, len(selected))
	for address := range selected {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	graph := ResourceGraph{APIVersion: "scenery.graph/v1", Focus: focus, Direction: options.Direction, Edges: []GraphEdge{}}
	if len(addresses) > options.MaxResources {
		graph.Truncated = true
		addresses = addresses[:options.MaxResources]
	}
	included := map[string]bool{}
	for _, address := range addresses {
		included[address] = true
		graph.Resources = append(graph.Resources, byAddress[address])
	}
	for _, edge := range allEdges {
		if included[edge.From] && included[edge.To] {
			graph.Edges = append(graph.Edges, edge)
		}
	}
	return graph, nil
}

func Context(manifest *Manifest, options ContextOptions) (ContextBundle, error) {
	return contextAt(manifest, "", nil, options, time.Now().UTC())
}

func ContextSnapshot(manifest *Manifest, workspaceRevision string, options ContextOptions) (ContextBundle, error) {
	return contextAt(manifest, workspaceRevision, nil, options, time.Now().UTC())
}

func ContextSnapshotWithDiagnostics(manifest *Manifest, workspaceRevision string, diagnostics []Diagnostic, options ContextOptions) (ContextBundle, error) {
	return contextAt(manifest, workspaceRevision, diagnostics, options, time.Now().UTC())
}

func contextAt(manifest *Manifest, workspaceRevision string, diagnostics []Diagnostic, options ContextOptions, now time.Time) (ContextBundle, error) {
	if manifest == nil {
		return ContextBundle{}, fmt.Errorf("failed_precondition: no contract snapshot is available")
	}
	if len(options.Focus) == 0 {
		return ContextBundle{}, fmt.Errorf("invalid_request: focus is required")
	}
	options.Focus = canonicalStrings(options.Focus)
	options.Include = canonicalStrings(options.Include)
	for _, include := range options.Include {
		if !oneOf(include, "dependencies", "dependents", "schemas", "diagnostics", "provenance") {
			return ContextBundle{}, fmt.Errorf("invalid_request: unsupported context include %q", include)
		}
	}
	if options.View == "" {
		options.View = "effective"
	}
	if options.MaxResources <= 0 {
		options.MaxResources = 100
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 200000
	}
	if options.Depth < 0 || options.Depth > agentMaxDepth {
		return ContextBundle{}, fmt.Errorf("invalid_request: depth must be between 0 and %d", agentMaxDepth)
	}
	if options.MaxResources > agentMaxResources {
		return ContextBundle{}, fmt.Errorf("invalid_request: max_resources exceeds transport limit %d", agentMaxResources)
	}
	if options.MaxBytes > agentMaxBytes {
		return ContextBundle{}, fmt.Errorf("invalid_request: max_bytes exceeds transport limit %d", agentMaxBytes)
	}
	queryDigest := contextQueryDigest(options)
	offset := 0
	if options.ContinuationToken != "" {
		payload, err := parseContextToken(options.ContinuationToken)
		if err != nil {
			return ContextBundle{}, fmt.Errorf("failed_precondition: invalid continuation token")
		}
		if payload.WorkspaceRevision != workspaceRevision || payload.ContractRevision != manifest.ContractRevision {
			return ContextBundle{}, fmt.Errorf("failed_precondition: continuation snapshot is unavailable")
		}
		if payload.QueryDigest != queryDigest {
			return ContextBundle{}, fmt.Errorf("failed_precondition: continuation token does not match the query")
		}
		if !now.Before(time.Unix(payload.ExpiresUnix, 0)) {
			return ContextBundle{}, fmt.Errorf("failed_precondition: continuation token expired")
		}
		offset = payload.Offset
	}
	selected := map[string]Resource{}
	for _, focus := range options.Focus {
		direction := contextDirection(options.Include)
		graph, err := graph(manifest, focus, GraphOptions{Direction: direction, Depth: options.Depth, MaxResources: len(manifest.Resources)}, false)
		if err != nil {
			return ContextBundle{}, err
		}
		for _, resource := range graph.Resources {
			selected[resource.Address] = resource
		}
	}
	addresses := make([]string, 0, len(selected))
	for address := range selected {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	if offset < 0 || offset > len(addresses) {
		return ContextBundle{}, fmt.Errorf("failed_precondition: continuation offset is unavailable")
	}
	bundle := ContextBundle{APIVersion: "scenery.context/v1", WorkspaceRevision: workspaceRevision, ContractRevision: manifest.ContractRevision, View: options.View, Resources: []Resource{}}
	for _, address := range addresses[offset:] {
		candidate := bundle
		candidate.Resources = append(append([]Resource(nil), bundle.Resources...), selected[address])
		populateContextIncludes(&candidate, options.Include, diagnostics)
		encoded, _ := json.Marshal(candidate)
		if len(candidate.Resources) > options.MaxResources || len(encoded) > options.MaxBytes {
			bundle.Truncated = true
			break
		}
		bundle = candidate
	}
	populateContextIncludes(&bundle, options.Include, diagnostics)
	for {
		nextOffset := offset + len(bundle.Resources)
		bundle.Truncated = nextOffset < len(addresses)
		bundle.ContinuationToken = ""
		if bundle.Truncated {
			bundle.ContinuationToken = makeContextToken(contextTokenPayload{
				Version: 1, WorkspaceRevision: workspaceRevision, ContractRevision: manifest.ContractRevision,
				QueryDigest: queryDigest, Offset: nextOffset, ExpiresUnix: now.Add(contextTokenTTL).Unix(),
			})
		}
		encoded, _ := json.Marshal(bundle)
		if len(encoded) <= options.MaxBytes {
			break
		}
		if len(bundle.Resources) == 0 {
			return ContextBundle{}, fmt.Errorf("invalid_request: max_bytes is too small for one resource and continuation metadata")
		}
		bundle.Resources = bundle.Resources[:len(bundle.Resources)-1]
		populateContextIncludes(&bundle, options.Include, diagnostics)
	}
	if len(bundle.Resources) == 0 && offset < len(addresses) {
		return ContextBundle{}, fmt.Errorf("invalid_request: max_bytes is too small for one resource")
	}
	return bundle, nil
}

func populateContextIncludes(bundle *ContextBundle, include []string, diagnostics []Diagnostic) {
	wants := map[string]bool{}
	for _, value := range include {
		wants[value] = true
	}
	selected := map[string]bool{}
	if wants["schemas"] {
		bundle.Schemas = map[string]map[string]any{}
	} else {
		bundle.Schemas = nil
	}
	if wants["provenance"] {
		bundle.Provenance = map[string]Origin{}
	} else {
		bundle.Provenance = nil
	}
	for _, resource := range bundle.Resources {
		selected[resource.Address] = true
		if wants["schemas"] {
			if schema, ok := CoreSchema(resource.Kind); ok {
				bundle.Schemas[resource.Kind] = schema
			}
		}
		if wants["provenance"] {
			bundle.Provenance[resource.Address] = resource.Origin
		}
	}
	if wants["diagnostics"] {
		bundle.Diagnostics = bundle.Diagnostics[:0]
		for _, diagnostic := range diagnostics {
			if diagnostic.Address == "" || selected[diagnostic.Address] {
				bundle.Diagnostics = append(bundle.Diagnostics, diagnostic)
			}
		}
	}
}

func resourceEdges(resources []Resource) []GraphEdge {
	known := resourcesByAddress(&Manifest{Resources: resources})
	var edges []GraphEdge
	for _, resource := range resources {
		walkRefs(resource.Spec, "/spec", func(path, reference string) {
			address := resolveGraphReference(resource, reference)
			if known[address].Address != "" {
				edges = append(edges, GraphEdge{From: resource.Address, To: address, Path: path})
			}
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Path < edges[j].Path
	})
	return edges
}

func walkRefs(value any, path string, visit func(string, string)) {
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			visit(path, reference)
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkRefs(typed[key], path+"/"+escapeJSONPointer(key), visit)
		}
	case []any:
		for index, item := range typed {
			walkRefs(item, fmt.Sprintf("%s/%d", path, index), visit)
		}
	}
}

func resolveGraphReference(resource Resource, reference string) string {
	if strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 {
		return ""
	}
	module := resource.Module
	if rootResourceKinds[parts[0]] {
		module = "app"
	}
	return resourceAddress(module, parts[0], parts[1])
}

func contextDirection(include []string) string {
	dependencies, dependents := false, false
	for _, item := range include {
		dependencies = dependencies || item == "dependencies"
		dependents = dependents || item == "dependents"
	}
	if dependencies && !dependents {
		return "dependencies"
	}
	if dependents && !dependencies {
		return "dependents"
	}
	return "both"
}

type contextTokenPayload struct {
	Version           int    `json:"v"`
	WorkspaceRevision string `json:"w"`
	ContractRevision  string `json:"c"`
	QueryDigest       string `json:"q"`
	Offset            int    `json:"o"`
	ExpiresUnix       int64  `json:"e"`
}

func contextQueryDigest(options ContextOptions) string {
	options.ContinuationToken = ""
	b, _ := json.Marshal(struct {
		Focus        []string `json:"focus"`
		Include      []string `json:"include"`
		Depth        int      `json:"depth"`
		MaxResources int      `json:"max_resources"`
		MaxBytes     int      `json:"max_bytes"`
		View         string   `json:"view"`
	}{options.Focus, options.Include, options.Depth, options.MaxResources, options.MaxBytes, options.View})
	sum := sha256.Sum256(append([]byte("scenery.context-query.v1\x00"), b...))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func makeContextToken(payload contextTokenPayload) string {
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte("scenery.context-continuation.v1\x00"), b...))
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sum[:])
}

func parseContextToken(token string) (contextTokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return contextTokenPayload{}, fmt.Errorf("invalid token")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return contextTokenPayload{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return contextTokenPayload{}, err
	}
	want := sha256.Sum256(append([]byte("scenery.context-continuation.v1\x00"), b...))
	if !bytes.Equal(signature, want[:]) {
		return contextTokenPayload{}, fmt.Errorf("invalid token checksum")
	}
	var payload contextTokenPayload
	if err := json.Unmarshal(b, &payload); err != nil || payload.Version != 1 || payload.Offset < 0 || payload.ExpiresUnix <= 0 {
		return contextTokenPayload{}, fmt.Errorf("invalid token payload")
	}
	return payload, nil
}
