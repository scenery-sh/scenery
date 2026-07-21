package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
)

type Kind string
type Revision string

type DiagnosticRule struct {
	Code             string   `json:"code"`
	Category         string   `json:"category"`
	Identity         string   `json:"identity"`
	DefaultSeverity  string   `json:"default_severity"`
	StructuredFields []string `json:"structured_fields"`
}

type SemanticRevisions struct {
	SourceComposition    Revision `json:"source_composition"`
	Defaults             Revision `json:"defaults"`
	Expansion            Revision `json:"expansion"`
	ReferenceResolution  Revision `json:"reference_resolution"`
	ContractProjection   Revision `json:"contract_projection"`
	EvolutionRules       Revision `json:"evolution_rules"`
	GoGeneration         Revision `json:"go_generation"`
	TypeScriptGeneration Revision `json:"typescript_generation"`
}

type Catalog struct {
	ResourceSchemas   map[Kind]map[string]any   `json:"resource_schemas"`
	StructuralSchemas map[string]map[string]any `json:"structural_schemas"`
	DiagnosticRules   map[string]DiagnosticRule `json:"diagnostic_rules"`
	Semantics         SemanticRevisions         `json:"semantics"`
}

func Current() Catalog {
	resources := make(map[Kind]map[string]any, len(resourceSchemas))
	for kind := range resourceSchemas {
		schema, ok := CoreSchema(kind)
		if !ok {
			panic("missing public schema for " + kind)
		}
		resources[Kind(kind)] = schema
	}
	structural := make(map[string]map[string]any, len(authoredStructuralSchemas))
	for name, schema := range authoredStructuralSchemas {
		structural[name] = publicAuthoredBlockSchema(schema)
	}
	diagnostics := make(map[string]DiagnosticRule, len(diagnosticDefinitions))
	for _, definition := range DiagnosticDefinitions() {
		diagnostics[definition.Code] = DiagnosticRule{
			Code: definition.Code, Category: definition.Category, Identity: definition.Identity,
			DefaultSeverity: definition.DefaultSeverity, StructuredFields: append([]string(nil), definition.StructuredFields...),
		}
	}
	return Catalog{ResourceSchemas: resources, StructuralSchemas: structural, DiagnosticRules: diagnostics, Semantics: CurrentSemanticRevisions()}
}

// CurrentSemanticRevisions are explicit review gates for behavior that is not
// fully represented by declarative source/resource schemas. Change the owning
// digest whenever that behavior changes.
func CurrentSemanticRevisions() SemanticRevisions {
	return SemanticRevisions{
		SourceComposition:    "sha256:d7b7bf5f2f7187f43cabf92d66774f44cdef8a15ac53dbf30b4048a88461df5e",
		Defaults:             "sha256:624f80596718cc9cfc71ddbee9989204b485d98ab96e517dfdf7ba549f3ab685",
		Expansion:            "sha256:a1ff16940d253b7c290e5e5f4f9af146fae30bad0e2cf00a887d30b8dd8a8dd1",
		ReferenceResolution:  "sha256:7ecc0fc75e31d0a9a6c69ba50ae2817b69f0ae9447d7648b2188d9786724e89b",
		ContractProjection:   "sha256:35bf6a93c2b8acbf829a6253c7aba39bedb6d41c708ec213f5454a1bbf455fcc",
		EvolutionRules:       "sha256:b143c9be9c74f9c3542a56d7ea4dd92ca05dd711a472cc4c3f24dc51e81ce481",
		GoGeneration:         "sha256:ab020bfc57f634dcdcb0931903ab6a636ae4f77fbf9d5749a348a44ac73ab7a7",
		TypeScriptGeneration: "sha256:590c81189319c36baa1fb4cc274ef91a5db62263ff1df3915785527d5a8a3311",
	}
}

func RevisionOf(catalog Catalog) Revision {
	return revision("scenery.spec", catalog)
}

// currentRevision is computed once: the catalog is a pure function of the
// binary, and recomputing the canonical hash per artifact identity dominated
// the long-running agent's CPU profile.
var currentRevision = sync.OnceValue(func() Revision {
	return RevisionOf(Current())
})

func CurrentRevision() Revision {
	return currentRevision()
}

func SchemaRevision(schema any) Revision {
	return revision("scenery.schema", schema)
}

// SchemaDocumentRevision hashes the complete JSON Schema shape while
// normalizing the schema's self-referential revision constant and location.
func SchemaDocumentRevision(document []byte) (Revision, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("schema document contains trailing JSON")
		}
		return "", err
	}
	delete(schema, "$id")
	properties, _ := schema["properties"].(map[string]any)
	identity, _ := properties["schema_revision"].(map[string]any)
	if identity == nil {
		return "", fmt.Errorf("schema document has no schema_revision property")
	}
	identity["const"] = "sha256:self"
	return SchemaRevision(schema), nil
}

func revision(domain string, value any) Revision {
	encoded, err := MarshalCanonical(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(append([]byte(domain+"\x00"), encoded...))
	return Revision("sha256:" + hex.EncodeToString(digest[:]))
}

func Kinds() []Kind {
	kinds := make([]Kind, 0, len(resourceSchemas))
	for kind := range resourceSchemas {
		kinds = append(kinds, Kind(kind))
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
