package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Edition           = "2027"
	ManifestVersion   = "scenery.manifest.v1"
	DiagnosticCatalog = "scenery.diagnostics.2027.v1"
)

var KernelProfiles = []string{
	"scenery.compiler-core/v1",
	"scenery.go-implementation/v1",
	"scenery.http-codec/v1",
	"scenery.runtime-http/v1",
	"scenery.inspection-core/v1",
}

var SupportedProfiles = map[string]bool{
	"scenery.compiler-core/v1":      true,
	"scenery.go-implementation/v1":  true,
	"scenery.http-codec/v1":         true,
	"scenery.runtime-http/v1":       true,
	"scenery.runtime-durable/v1":    true,
	"scenery.events/v1":             true,
	"scenery.data/v1":               true,
	"scenery.deployment/v1":         true,
	"scenery.inspection-core/v1":    true,
	"scenery.agent-read/v1":         true,
	"scenery.agent-mutation/v1":     true,
	"scenery.patches/v1":            true,
	"scenery.ui/v1":                 true,
	"scenery.legacy-bridge/v1":      true,
	"scenery.compatibility-core/v1": true,
	"scenery.typescript-client/v1":  true,
}

var ProfileDependencies = map[string][]string{
	"scenery.go-implementation/v1":  {"scenery.compiler-core/v1"},
	"scenery.http-codec/v1":         {"scenery.compiler-core/v1"},
	"scenery.runtime-http/v1":       {"scenery.compiler-core/v1", "scenery.go-implementation/v1", "scenery.http-codec/v1"},
	"scenery.runtime-durable/v1":    {"scenery.compiler-core/v1", "scenery.go-implementation/v1"},
	"scenery.events/v1":             {"scenery.compiler-core/v1"},
	"scenery.data/v1":               {"scenery.compiler-core/v1"},
	"scenery.deployment/v1":         {"scenery.compiler-core/v1", "scenery.compatibility-core/v1"},
	"scenery.inspection-core/v1":    {"scenery.compiler-core/v1"},
	"scenery.agent-read/v1":         {"scenery.inspection-core/v1", "scenery.compatibility-core/v1"},
	"scenery.agent-mutation/v1":     {"scenery.agent-read/v1"},
	"scenery.patches/v1":            {"scenery.compiler-core/v1"},
	"scenery.ui/v1":                 {"scenery.compiler-core/v1", "scenery.data/v1"},
	"scenery.legacy-bridge/v1":      {"scenery.compiler-core/v1", "scenery.compatibility-core/v1"},
	"scenery.compatibility-core/v1": {"scenery.compiler-core/v1"},
	"scenery.typescript-client/v1":  {"scenery.compiler-core/v1", "scenery.compatibility-core/v1", "scenery.http-codec/v1"},
}

type Position struct {
	Line       int `json:"line"`
	Column     int `json:"column"`
	ByteOffset int `json:"byte_offset"`
}

type Range struct {
	SourceID string   `json:"source_id"`
	Start    Position `json:"start"`
	End      Position `json:"end"`
}

type Diagnostic struct {
	Code        string         `json:"code"`
	Severity    string         `json:"severity"`
	Message     string         `json:"message"`
	ReportToken string         `json:"report_token,omitempty"`
	Address     string         `json:"address,omitempty"`
	Path        string         `json:"path,omitempty"`
	Range       *Range         `json:"range,omitempty"`
	Related     []Related      `json:"related,omitempty"`
	Suggestions []string       `json:"suggestions,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type Related struct {
	Address string `json:"address,omitempty"`
	Path    string `json:"path,omitempty"`
}

type Origin struct {
	Kind             string           `json:"kind"`
	SourceID         string           `json:"source_id,omitempty"`
	Frontend         string           `json:"frontend,omitempty"`
	LegacySymbol     string           `json:"legacy_symbol,omitempty"`
	LegacyConstruct  string           `json:"legacy_construct,omitempty"`
	LegacyIdentity   map[string]any   `json:"legacy_identity,omitempty"`
	Patches          []string         `json:"patches,omitempty"`
	DeclarationRange *Range           `json:"declaration_range,omitempty"`
	AttributeRanges  map[string]Range `json:"attribute_ranges,omitempty"`
	ModuleChain      []string         `json:"module_instantiation_chain,omitempty"`
	ExpansionLineage []ExpansionStep  `json:"expansion_lineage,omitempty"`
}

type ExpansionStep struct {
	Generator               string `json:"generator"`
	GeneratorSchemaRevision string `json:"generator_schema_revision"`
	Key                     string `json:"key"`
	SourceRange             *Range `json:"source_range,omitempty"`
	ParentAddress           string `json:"parent_address"`
	Output                  string `json:"output"`
}

type Resource struct {
	Address       string               `json:"address"`
	Kind          string               `json:"kind"`
	Name          string               `json:"name"`
	Module        string               `json:"module"`
	Spec          map[string]any       `json:"spec"`
	Origin        Origin               `json:"origin"`
	Migration     *MigrationMeta       `json:"migration,omitempty"`
	Compatibility *LegacyCompatibility `json:"compatibility,omitempty"`
}

type LegacyCompatibility struct {
	Semantics            string `json:"semantics"`
	Contract             string `json:"contract"`
	MigrationDisposition string `json:"migration_disposition"`
}

type MigrationMeta struct {
	State           string `json:"state"`
	Active          string `json:"active"`
	NativeCandidate string `json:"native_candidate,omitempty"`
}

type ApplicationIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Manifest struct {
	APIVersion        string                  `json:"api_version"`
	Edition           string                  `json:"edition"`
	DiagnosticCatalog string                  `json:"diagnostic_catalog"`
	Application       ApplicationIdentity     `json:"application"`
	Profiles          []string                `json:"profiles"`
	ContractRevision  string                  `json:"contract_revision"`
	Resources         []Resource              `json:"resources"`
	SourceMap         map[string]SourceRecord `json:"source_map"`
	Diagnostics       []Diagnostic            `json:"diagnostics"`
}

type SourceRecord struct {
	URI string `json:"uri"`
}

type Result struct {
	Root                    string               `json:"-"`
	Manifest                *Manifest            `json:"manifest,omitempty"`
	ViewManifests           map[string]*Manifest `json:"-"`
	PartialGraph            *PartialGraph        `json:"partial_graph,omitempty"`
	ContractStatus          string               `json:"contract_status"`
	ImplementationStatus    string               `json:"implementation_status"`
	WorkspaceRevision       string               `json:"workspace_revision"`
	ImplementationRevisions map[string]string    `json:"implementation_revision,omitempty"`
	DeploymentRevisions     map[string]string    `json:"deployment_revision,omitempty"`
	HTTPSurfaceRevisions    map[string]string    `json:"http_surface_revision,omitempty"`
	OpenAPIRevisions        map[string]string    `json:"openapi_revision,omitempty"`
	Diagnostics             []Diagnostic         `json:"diagnostics"`
	Sources                 []*Source            `json:"-"`
	Migration               *Migration           `json:"migration,omitempty"`
	verifiedGoFiles         []generatedFile
	hasVerifiedGoFiles      bool
}

// ManifestForView returns the immutable compiler snapshot for one of the
// edition-defined graph views. The expanded view is the deployable manifest.
func (r *Result) ManifestForView(view string) (*Manifest, error) {
	if view == "" {
		view = "expanded"
	}
	if view != "source" && view != "effective" && view != "expanded" {
		return nil, fmt.Errorf("invalid_request: unsupported graph view %q", view)
	}
	if r == nil || r.Manifest == nil {
		return nil, fmt.Errorf("failed_precondition: no valid manifest is available")
	}
	if view == "expanded" || r.ViewManifests == nil {
		return r.Manifest, nil
	}
	manifest := r.ViewManifests[view]
	if manifest == nil {
		return nil, fmt.Errorf("failed_precondition: graph view %q is unavailable", view)
	}
	return manifest, nil
}

type PartialGraph struct {
	Deployable  bool                    `json:"deployable"`
	Application ApplicationIdentity     `json:"application"`
	Profiles    []string                `json:"profiles"`
	Resources   []Resource              `json:"resources"`
	SourceMap   map[string]SourceRecord `json:"source_map"`
}

func (r *Result) Valid() bool {
	if r == nil || r.ContractStatus != "valid" || r.Manifest == nil {
		return false
	}
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			return false
		}
	}
	return true
}

func canonicalResources(resources []Resource) ([]byte, error) {
	copyResources := append([]Resource(nil), resources...)
	sort.Slice(copyResources, func(i, j int) bool { return copyResources[i].Address < copyResources[j].Address })
	return MarshalCanonical(copyResources)
}

func contractRevision(resources []Resource, profiles []string, appName string) (string, error) {
	projected := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		contractResource, include := contractResourceProjection(resource)
		if !include {
			continue
		}
		projected = append(projected, contractResource)
	}
	sort.Strings(profiles)
	value := struct {
		Edition      string           `json:"edition"`
		Application  string           `json:"application"`
		Profiles     []string         `json:"profiles"`
		Dependencies []map[string]any `json:"compile_dependencies"`
		Resources    []Resource       `json:"resources"`
	}{Edition: Edition, Application: appName, Profiles: profiles, Dependencies: dependencyContractIdentities(resources), Resources: projected}
	b, err := MarshalCanonical(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("scenery.contract-revision.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func dependencyContractIdentities(resources []Resource) []map[string]any {
	var identities []map[string]any
	for _, resource := range resources {
		if resource.Kind != "scenery.provider/v1" && resource.Kind != "scenery.module/v1" || resource.Spec["compile_descriptor_digest"] == nil {
			continue
		}
		identities = append(identities, map[string]any{
			"kind":   strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery."),
			"source": resource.Spec["source"], "version": resource.Spec["locked_version"],
			"integrity": resource.Spec["locked_integrity"], "compile_descriptor_digest": resource.Spec["compile_descriptor_digest"],
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		if stringValue(identities[i]["kind"]) != stringValue(identities[j]["kind"]) {
			return stringValue(identities[i]["kind"]) < stringValue(identities[j]["kind"])
		}
		return stringValue(identities[i]["source"]) < stringValue(identities[j]["source"])
	})
	return identities
}

func contractResourceProjection(resource Resource) (Resource, bool) {
	schema, ok := resourceSchemas[resource.Kind]
	if !ok {
		return Resource{}, false
	}
	projected := Resource{Address: resource.Address, Kind: resource.Kind, Name: resource.Name, Module: resource.Module, Spec: make(map[string]any, len(resource.Spec))}
	for key, value := range resource.Spec {
		if rule, dynamic := dynamicResourceRevisionDomains[resource.Kind][key]; dynamic {
			if contractValue, include := dynamicContractFieldProjection(resource, key, value, rule); include {
				projected.Spec[key] = contractValue
			}
			continue
		}
		if domain, exists := resourceFieldRevisionDomain(resource.Kind, key); exists && domain == "contract" {
			projected.Spec[key] = value
		}
	}
	if schema.RevisionDomain != "contract" && len(projected.Spec) == 0 {
		return Resource{}, false
	}
	return projected, true
}

func dynamicContractFieldProjection(resource Resource, field string, value any, rule dynamicRevisionDomain) (any, bool) {
	domains := map[string]string{}
	for _, descriptor := range namedChildren(resource.Spec, rule.SchemaField) {
		domains[stringValue(descriptor[rule.NameField])] = stringValue(descriptor[rule.DomainField])
	}
	if field == rule.SchemaField {
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		projected := make([]any, 0, len(items))
		for _, item := range items {
			descriptor, ok := item.(map[string]any)
			if ok && stringValue(descriptor[rule.DomainField]) == "contract" {
				projected = append(projected, item)
			}
		}
		return projected, len(projected) > 0
	}
	values, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	projected := map[string]any{}
	for name, item := range values {
		if domains[name] == "contract" {
			projected[name] = item
		}
	}
	return projected, len(projected) > 0
}

func kindForBlock(blockType string) string {
	switch blockType {
	case "binding":
		return "scenery.binding/v1"
	default:
		return "scenery." + strings.ReplaceAll(blockType, "_", "-") + "/v1"
	}
}

func resourceAddress(module, blockType, name string) string {
	if module == "" {
		module = "app"
	}
	return filepath.ToSlash(fmt.Sprintf("%s/%s/%s", module, blockType, name))
}

func moduleResourceAddress(instance string) string {
	parts := strings.Split(strings.Trim(instance, "/"), "/")
	if len(parts) <= 1 {
		return resourceAddress("app", "module", instance)
	}
	return resourceAddress(strings.Join(parts[:len(parts)-1], "/"), "module", parts[len(parts)-1])
}
