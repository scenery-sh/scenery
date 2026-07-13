package contractagent

import (
	"sort"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

var mutationSchemaRevisions = map[string]string{
	"resource.create":  "scenery.mutation.resource-create",
	"resource.delete":  "scenery.mutation.resource-delete",
	"resource.rename":  "scenery.mutation.resource-rename",
	"value.set":        "scenery.mutation.value-set",
	"value.unset":      "scenery.mutation.value-unset",
	"module.configure": "scenery.mutation.module-configure",
}

func AgentSchema(name string) (map[string]any, bool) {
	if schema, ok := spec.CoreSchema(name); ok {
		return schema, true
	}
	if schema, ok := spec.AuthoredPublicSchema(name); ok {
		return schema, true
	}
	if name == graph.DiagnosticCatalog {
		return map[string]any{"schema_revision": graph.DiagnosticCatalog, "type": "diagnostic_catalog", "definitions": spec.DiagnosticDefinitions()}, true
	}
	if definition, ok := spec.DiagnosticDefinitionFor(name); ok {
		return map[string]any{
			"schema_revision": graph.DiagnosticCatalog, "type": "diagnostic_definition", "code": definition.Code, "category": definition.Category,
			"identity": definition.Identity, "meaning": definition.Meaning, "default_severity": definition.DefaultSeverity,
			"structured_fields": definition.StructuredFields, "documentation": definition.Documentation,
		}, true
	}
	switch name {
	case "scenery.value":
		return revisionedAgentSchema(map[string]any{
			"kind":        "scenery.value",
			"description": "Canonical typed Scenery value",
			"one_of": []any{
				map[string]any{"type": "null"}, map[string]any{"type": "boolean"}, map[string]any{"type": "string"},
				map[string]any{"type": "array", "items": map[string]any{"$ref": "scenery.value"}},
				map[string]any{"type": "object", "additional_properties": map[string]any{"$ref": "scenery.value"}},
				map[string]any{"type": "object", "required": []string{"$scalar"}, "description": "Exact scalar envelope"},
				map[string]any{"type": "object", "required": []string{"$ref"}, "description": "Typed resource reference"},
			},
		}), true
	case "scenery.diagnostic":
		return revisionedAgentSchema(map[string]any{
			"kind": "scenery.diagnostic", "type": "object",
			"required": []string{"code", "severity", "message"},
			"properties": map[string]any{
				"code": map[string]any{"type": "string"}, "severity": map[string]any{"enum": []string{"error", "warning", "information", "hint"}},
				"message": map[string]any{"type": "string"}, "report_token": map[string]any{"type": "string", "pattern": "^rpt_[a-z2-7]+$"}, "address": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
				"range": map[string]any{"schema_revision": "scenery.source-range"}, "related": map[string]any{"type": "array"}, "suggestions": map[string]any{"type": "array"}, "fixes": map[string]any{"type": "array"},
			},
		}), true
	case "scenery.semantic-operation":
		return semanticOperationSchema("scenery.semantic-operation", ""), true
	}
	if revision, ok := mutationSchemaRevisions[name]; ok {
		return semanticOperationSchema(revision, name), true
	}
	for operation, kind := range mutationSchemaRevisions {
		schema := semanticOperationSchema(kind, operation)
		if name == kind || name == stringValue(schema["schema_revision"]) {
			return schema, true
		}
	}
	for _, kind := range []string{"scenery.value", "scenery.diagnostic", "scenery.semantic-operation"} {
		schema, _ := AgentSchema(kind)
		if name == stringValue(schema["schema_revision"]) {
			return schema, true
		}
	}
	return nil, false
}

func semanticOperationSchema(revision, operation string) map[string]any {
	op := map[string]any{"type": "string", "enum": canonicalStrings(mapKeys(mutationSchemaRevisions))}
	if operation != "" {
		op = map[string]any{"const": operation}
	}
	properties := map[string]any{
		"op": op, "address": map[string]any{"type": "string"}, "view": map[string]any{"enum": []string{"source"}},
		"expected_kind": map[string]any{"type": "string"}, "expected_schema_revision": map[string]any{"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
		"path": map[string]any{"type": "string", "format": "json-pointer", "pattern": "^/spec(?:/|$)"}, "value": map[string]any{"$ref": "scenery.value"},
		"precondition": map[string]any{"type": "object", "additional_properties": false, "properties": map[string]any{"exists": map[string]any{"type": "boolean"}, "absent": map[string]any{"type": "boolean"}, "equals": map[string]any{"$ref": "scenery.value"}}},
	}
	required := []string{"op", "address"}
	switch operation {
	case "resource.create":
		required = append(required, "value")
		properties["value"] = map[string]any{"type": "object", "additional_properties": map[string]any{"$ref": "scenery.value"}}
		delete(properties, "path")
		delete(properties, "precondition")
	case "resource.delete":
		delete(properties, "path")
		delete(properties, "value")
	case "resource.rename":
		required = append(required, "value")
		properties["value"] = map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9_]*$"}
		delete(properties, "path")
	case "value.set":
		required = append(required, "path", "value")
	case "value.unset":
		required = append(required, "path")
		delete(properties, "value")
	case "module.configure":
		required = append(required, "value")
		properties["value"] = map[string]any{"type": "object", "additional_properties": map[string]any{"$ref": "scenery.value"}}
		delete(properties, "path")
	}
	result := map[string]any{
		"kind": revision, "type": "object", "additional_properties": false,
		"required": required, "properties": properties,
	}
	if operation == "resource.create" {
		result["resource_kinds"] = spec.ResourceCreateSchemaRevisions()
	}
	return revisionedAgentSchema(result)
}

func revisionedAgentSchema(schema map[string]any) map[string]any {
	schema["schema_revision"] = string(spec.SchemaRevision(schema))
	return schema
}

func sourceSchemaRevisionForInternalName(name string) string {
	return string(spec.SourceSchemaRevisionForInternalName(name))
}

func allResourceSchemaRevisions() []string {
	resourceSchemas := spec.ResourceSchemas()
	values := make([]string, 0, len(resourceSchemas))
	for kind := range resourceSchemas {
		schema, _ := spec.CoreSchema(kind)
		values = append(values, stringValue(schema["schema_revision"]))
	}
	sort.Strings(values)
	return values
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func allMutationSchemaRevisions() []string {
	values := []string{}
	for _, kind := range []string{"scenery.value", "scenery.diagnostic", "scenery.semantic-operation"} {
		schema, _ := AgentSchema(kind)
		values = append(values, stringValue(schema["schema_revision"]))
	}
	for operation, kind := range mutationSchemaRevisions {
		values = append(values, stringValue(semanticOperationSchema(kind, operation)["schema_revision"]))
	}
	sort.Strings(values)
	return values
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
