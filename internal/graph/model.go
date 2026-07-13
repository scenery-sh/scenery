package graph

import (
	"fmt"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/spec"
)

const ManifestKind = "scenery.manifest"

var (
	ManifestSchemaRevision = "sha256:d5b3c19523c452c5fafe25bceac20c42ab1d6de2d285a11615c7d24f21c2c3c7"
	DiagnosticCatalog      = string(spec.SchemaRevision(spec.DiagnosticDefinitions()))
)

type Diagnostic struct {
	Code        string         `json:"code"`
	Severity    string         `json:"severity"`
	Message     string         `json:"message"`
	ReportToken string         `json:"report_token,omitempty"`
	Address     string         `json:"address,omitempty"`
	Path        string         `json:"path,omitempty"`
	Range       *scn.Range     `json:"range,omitempty"`
	Related     []Related      `json:"related,omitempty"`
	Suggestions []string       `json:"suggestions,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type Related struct {
	Address string `json:"address,omitempty"`
	Path    string `json:"path,omitempty"`
}

type Origin struct {
	Kind             string                     `json:"kind"`
	SourceID         string                     `json:"source_id,omitempty"`
	Patches          []string                   `json:"patches,omitempty"`
	DeclarationRange *scn.Range                 `json:"declaration_range,omitempty"`
	AttributeRanges  map[string]scn.Range       `json:"attribute_ranges,omitempty"`
	ModuleChain      []string                   `json:"module_instantiation_chain,omitempty"`
	ExpansionLineage []ExpansionStep            `json:"expansion_lineage,omitempty"`
	FieldProvenance  map[string]FieldProvenance `json:"field_provenance,omitempty"`
}

type FieldProvenance struct {
	Kind            string     `json:"kind"`
	DeclaredAt      *scn.Range `json:"declared_at,omitempty"`
	Input           string     `json:"input,omitempty"`
	ProvidedBy      string     `json:"provided_by,omitempty"`
	SourceAddress   string     `json:"source_address,omitempty"`
	Transformations []string   `json:"transformations,omitempty"`
}

type ExpansionStep struct {
	Generator               string     `json:"generator"`
	GeneratorSchemaRevision string     `json:"generator_schema_revision"`
	Key                     string     `json:"key"`
	SourceRange             *scn.Range `json:"source_range,omitempty"`
	ParentAddress           string     `json:"parent_address"`
	Output                  string     `json:"output"`
}

type Resource struct {
	Address string         `json:"address"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Module  string         `json:"module"`
	Spec    map[string]any `json:"spec"`
	Origin  Origin         `json:"origin"`
}

type ApplicationIdentity struct {
	Name string `json:"name"`
}

type Manifest struct {
	Kind              string                  `json:"kind"`
	SchemaRevision    string                  `json:"schema_revision"`
	SpecRevision      string                  `json:"spec_revision"`
	Producer          machine.Producer        `json:"producer"`
	DiagnosticCatalog string                  `json:"diagnostic_catalog"`
	Application       ApplicationIdentity     `json:"application"`
	ContractRevision  string                  `json:"contract_revision"`
	Resources         []Resource              `json:"resources"`
	SourceMap         map[string]SourceRecord `json:"source_map"`
	Diagnostics       []Diagnostic            `json:"diagnostics"`
}

type SourceRecord struct {
	URI string `json:"uri"`
}

type PartialGraph struct {
	Deployable  bool                    `json:"deployable"`
	Application ApplicationIdentity     `json:"application"`
	Resources   []Resource              `json:"resources"`
	SourceMap   map[string]SourceRecord `json:"source_map"`
}

// ManifestForView selects one immutable compiler graph view.
func ManifestForView(expanded *Manifest, views map[string]*Manifest, view string) (*Manifest, error) {
	if view == "" {
		view = "expanded"
	}
	if view != "source" && view != "effective" && view != "expanded" {
		return nil, fmt.Errorf("invalid_request: unsupported graph view %q", view)
	}
	if expanded == nil {
		return nil, fmt.Errorf("failed_precondition: no valid manifest is available")
	}
	if view == "expanded" || views == nil {
		return expanded, nil
	}
	manifest := views[view]
	if manifest == nil {
		return nil, fmt.Errorf("failed_precondition: graph view %q is unavailable", view)
	}
	return manifest, nil
}
