package contractagent

import (
	"slices"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

var DiagnosticCatalog = graph.DiagnosticCatalog

func CoreSchema(kind string) (map[string]any, bool) { return spec.CoreSchema(kind) }

var resourceSchemas = spec.ResourceSchemas()

func resourceCreateSchemaRevisions() []string { return spec.ResourceCreateSchemaRevisions() }

func isCanonicalSHA256Digest(value string) bool { return graph.IsCanonicalSHA256Digest(value) }

func containsExactString(values []string, want string) bool { return slices.Contains(values, want) }

func sourceSchemaRevisionForInternalName(name string) string {
	return string(spec.SourceSchemaRevisionForInternalName(name))
}
