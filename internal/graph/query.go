package graph

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

const ContextTokenTTL = 5 * time.Minute

const (
	AgentMaxResources = 1000
	AgentMaxBytes     = 2_000_000
	AgentMaxDepth     = 16
)

const (
	resourceGraphKind             = "scenery.graph"
	resourceGraphSchemaDescriptor = `{"direction":"direction","edges":"array<edge>","focus":"address","kind":"scenery.graph","producer":"producer","resources":"array<resource>","schema_revision":"digest","spec_revision":"digest","truncated":"boolean"}`
	contextBundleKind             = "scenery.context"
	contextBundleSchemaDescriptor = `{"continuation_token":"optional_token","contract_revision":"revision","diagnostics":"optional_diagnostics","kind":"scenery.context","producer":"producer","provenance":"optional_provenance","resources":"array<resource>","schema_revision":"digest","schemas":"optional_schemas","spec_revision":"digest","truncated":"boolean","view":"view","workspace_revision":"revision"}`
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
	machine.ArtifactIdentity
	Focus     string      `json:"focus"`
	Direction string      `json:"direction"`
	Resources []Resource  `json:"resources"`
	Edges     []GraphEdge `json:"edges"`
	Truncated bool        `json:"truncated"`
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
	machine.ArtifactIdentity
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
	if enforceTransportLimits && options.Depth > AgentMaxDepth {
		return ResourceGraph{}, fmt.Errorf("depth exceeds transport limit %d", AgentMaxDepth)
	}
	if options.MaxResources <= 0 {
		options.MaxResources = 100
	}
	if enforceTransportLimits && options.MaxResources > AgentMaxResources {
		return ResourceGraph{}, fmt.Errorf("max_resources exceeds transport limit %d", AgentMaxResources)
	}
	if options.Direction != "dependencies" && options.Direction != "dependents" && options.Direction != "both" {
		return ResourceGraph{}, fmt.Errorf("direction must be dependencies, dependents, or both")
	}
	byAddress := resourcesByAddress(manifest)
	if _, ok := byAddress[focus]; !ok {
		return ResourceGraph{}, fmt.Errorf("resource %q not found", focus)
	}
	allEdges := ResourceEdges(manifest.Resources)
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
	graph := ResourceGraph{ArtifactIdentity: machine.NewArtifactIdentity(resourceGraphKind, resourceGraphSchemaDescriptor), Focus: focus, Direction: options.Direction, Edges: []GraphEdge{}}
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
	return ContextAt(manifest, "", nil, options, time.Now().UTC())
}

func ContextSnapshot(manifest *Manifest, workspaceRevision string, options ContextOptions) (ContextBundle, error) {
	return ContextAt(manifest, workspaceRevision, nil, options, time.Now().UTC())
}

func ContextSnapshotWithDiagnostics(manifest *Manifest, workspaceRevision string, diagnostics []Diagnostic, options ContextOptions) (ContextBundle, error) {
	return ContextAt(manifest, workspaceRevision, diagnostics, options, time.Now().UTC())
}

func ContextAt(manifest *Manifest, workspaceRevision string, diagnostics []Diagnostic, options ContextOptions, now time.Time) (ContextBundle, error) {
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
	if options.Depth < 0 || options.Depth > AgentMaxDepth {
		return ContextBundle{}, fmt.Errorf("invalid_request: depth must be between 0 and %d", AgentMaxDepth)
	}
	if options.MaxResources > AgentMaxResources {
		return ContextBundle{}, fmt.Errorf("invalid_request: max_resources exceeds transport limit %d", AgentMaxResources)
	}
	if options.MaxBytes > AgentMaxBytes {
		return ContextBundle{}, fmt.Errorf("invalid_request: max_bytes exceeds transport limit %d", AgentMaxBytes)
	}
	queryDigest := contextQueryDigest(options)
	offset := 0
	if options.ContinuationToken != "" {
		payload, err := ParseContextToken(options.ContinuationToken)
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
	bundle := ContextBundle{ArtifactIdentity: machine.NewArtifactIdentity(contextBundleKind, contextBundleSchemaDescriptor), WorkspaceRevision: workspaceRevision, ContractRevision: manifest.ContractRevision, View: options.View, Resources: []Resource{}}
	populateContextIncludes(&bundle, options.Include, diagnostics)
	budget := newContextByteBudget(bundle, options.Include, diagnostics)
	for _, address := range addresses[offset:] {
		if !budget.add(selected[address], options.MaxResources, options.MaxBytes) {
			bundle.Truncated = true
			break
		}
		bundle.Resources = append(bundle.Resources, selected[address])
	}
	populateContextIncludes(&bundle, options.Include, diagnostics)
	for {
		nextOffset := offset + len(bundle.Resources)
		bundle.Truncated = nextOffset < len(addresses)
		bundle.ContinuationToken = ""
		if bundle.Truncated {
			bundle.ContinuationToken = makeContextToken(ContextToken{
				WorkspaceRevision: workspaceRevision, ContractRevision: manifest.ContractRevision,
				QueryDigest: queryDigest, Offset: nextOffset, ExpiresUnix: now.Add(ContextTokenTTL).Unix(),
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

// contextByteBudget tracks the exact serialized size of a growing context
// bundle without re-marshaling the whole bundle per resource. Each candidate
// fragment (resource, origin, schema, diagnostic) is marshaled once, and the
// running total adds the same comma and field overhead encoding/json produces,
// including the omitempty transitions of the schemas, provenance, and
// diagnostics sections. The trailing trim loop in ContextAt still performs the
// authoritative full-bundle marshal check with continuation metadata.
type contextByteBudget struct {
	total           int
	resourceCount   int
	wantSchemas     bool
	wantProvenance  bool
	wantDiagnostics bool
	schemaCount     int
	schemaKinds     map[string]bool
	diagnosticCount int
	diagnosticSizes map[string][]int
}

func newContextByteBudget(bundle ContextBundle, include []string, diagnostics []Diagnostic) contextByteBudget {
	budget := contextByteBudget{
		resourceCount:   len(bundle.Resources),
		diagnosticCount: len(bundle.Diagnostics),
		schemaKinds:     map[string]bool{},
		diagnosticSizes: map[string][]int{},
	}
	for _, value := range include {
		budget.wantSchemas = budget.wantSchemas || value == "schemas"
		budget.wantProvenance = budget.wantProvenance || value == "provenance"
		budget.wantDiagnostics = budget.wantDiagnostics || value == "diagnostics"
	}
	encoded, _ := json.Marshal(bundle)
	budget.total = len(encoded)
	if budget.wantDiagnostics {
		for _, diagnostic := range diagnostics {
			if diagnostic.Address == "" {
				continue
			}
			encodedDiagnostic, _ := json.Marshal(diagnostic)
			budget.diagnosticSizes[diagnostic.Address] = append(budget.diagnosticSizes[diagnostic.Address], len(encodedDiagnostic))
		}
	}
	return budget
}

func (b *contextByteBudget) add(resource Resource, maxResources, maxBytes int) bool {
	encodedResource, _ := json.Marshal(resource)
	cost := len(encodedResource)
	if b.resourceCount > 0 {
		cost++ // comma between resources array elements
	}
	schemaKind := ""
	if b.wantSchemas && !b.schemaKinds[resource.Kind] {
		if schema, ok := spec.CoreSchema(resource.Kind); ok {
			encodedKind, _ := json.Marshal(resource.Kind)
			encodedSchema, _ := json.Marshal(schema)
			if b.schemaCount == 0 {
				cost += len(`,"schemas":{}`)
			} else {
				cost++ // comma between schema entries
			}
			cost += len(encodedKind) + 1 + len(encodedSchema)
			schemaKind = resource.Kind
		}
	}
	if b.wantProvenance {
		encodedAddress, _ := json.Marshal(resource.Address)
		encodedOrigin, _ := json.Marshal(resource.Origin)
		if b.resourceCount == 0 {
			cost += len(`,"provenance":{}`)
		} else {
			cost++ // comma between provenance entries
		}
		cost += len(encodedAddress) + 1 + len(encodedOrigin)
	}
	diagnosticSizes := b.diagnosticSizes[resource.Address]
	for index, size := range diagnosticSizes {
		if b.diagnosticCount+index == 0 {
			cost += len(`,"diagnostics":[]`)
		} else {
			cost++ // comma between diagnostics array elements
		}
		cost += size
	}
	if b.resourceCount+1 > maxResources || b.total+cost > maxBytes {
		return false
	}
	b.total += cost
	b.resourceCount++
	if schemaKind != "" {
		b.schemaKinds[schemaKind] = true
		b.schemaCount++
	}
	b.diagnosticCount += len(diagnosticSizes)
	return true
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
			if schema, ok := spec.CoreSchema(resource.Kind); ok {
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

func ResourceEdges(resources []Resource) []GraphEdge {
	known := resourcesByAddress(&Manifest{Resources: resources})
	var edges []GraphEdge
	for _, resource := range resources {
		WalkReferences(resource.Spec, "/spec", func(path, reference string) {
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

func WalkReferences(value any, path string, visit func(string, string)) {
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
			WalkReferences(typed[key], path+"/"+escapeJSONPointer(key), visit)
		}
	case []any:
		for index, item := range typed {
			WalkReferences(item, path+"/"+strconv.Itoa(index), visit)
		}
	}
}

func resolveGraphReference(resource Resource, reference string) string {
	if strings.Contains(reference, "/") {
		return reference
	}
	blockType, name, ok := strings.Cut(reference, ".")
	if !ok || strings.Contains(name, ".") {
		return ""
	}
	module := resource.Module
	if rootResourceKinds[blockType] {
		module = "app"
	}
	return ResourceAddress(module, blockType, name)
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

type ContextToken struct {
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
	sum := sha256.Sum256(append([]byte("scenery.context-query\x00"), b...))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func makeContextToken(payload ContextToken) string {
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte("scenery.context-continuation\x00"), b...))
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sum[:])
}

func ParseContextToken(token string) (ContextToken, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ContextToken{}, fmt.Errorf("invalid token")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ContextToken{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ContextToken{}, err
	}
	want := sha256.Sum256(append([]byte("scenery.context-continuation\x00"), b...))
	if !bytes.Equal(signature, want[:]) {
		return ContextToken{}, fmt.Errorf("invalid token checksum")
	}
	var payload ContextToken
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || decoder.Decode(&struct{}{}) != io.EOF || payload.Offset < 0 || payload.ExpiresUnix <= 0 {
		return ContextToken{}, fmt.Errorf("invalid token payload")
	}
	return payload, nil
}
