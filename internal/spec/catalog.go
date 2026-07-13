package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type Kind string
type Revision string

type Catalog struct {
	Resources   map[Kind]map[string]any         `json:"resources"`
	Diagnostics map[string]DiagnosticDefinition `json:"diagnostics"`
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
	diagnostics := make(map[string]DiagnosticDefinition, len(diagnosticDefinitions))
	for _, definition := range DiagnosticDefinitions() {
		diagnostics[definition.Code] = definition
	}
	return Catalog{Resources: resources, Diagnostics: diagnostics}
}

func RevisionOf(catalog Catalog) Revision {
	return revision("scenery.spec", catalog)
}

func CurrentRevision() Revision {
	return RevisionOf(Current())
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
