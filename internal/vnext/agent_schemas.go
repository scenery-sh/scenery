package vnext

import "sort"

var mutationSchemaRevisions = map[string]string{
	"resource.create":  "scenery.mutation.resource-create/v1",
	"resource.delete":  "scenery.mutation.resource-delete/v1",
	"resource.rename":  "scenery.mutation.resource-rename/v1",
	"value.set":        "scenery.mutation.value-set/v1",
	"value.unset":      "scenery.mutation.value-unset/v1",
	"module.configure": "scenery.mutation.module-configure/v1",
	"module.upgrade":   "scenery.mutation.module-upgrade/v1",
}

func AgentSchema(name string) (map[string]any, bool) {
	if schema, ok := CoreSchema(name); ok {
		return schema, true
	}
	if schema, ok := authoredPublicSchema(name); ok {
		return schema, true
	}
	if name == DiagnosticCatalog {
		return map[string]any{"schema_revision": DiagnosticCatalog, "type": "diagnostic_catalog", "definitions": DiagnosticDefinitions()}, true
	}
	if definition, ok := DiagnosticDefinitionFor(name); ok {
		return map[string]any{
			"schema_revision": DiagnosticCatalog, "type": "diagnostic_definition", "code": definition.Code, "category": definition.Category,
			"identity": definition.Identity, "meaning": definition.Meaning, "default_severity": definition.DefaultSeverity,
			"structured_fields": definition.StructuredFields, "documentation": definition.Documentation,
		}, true
	}
	switch name {
	case "scenery.value/v1":
		return map[string]any{
			"schema_revision": "scenery.value/v1",
			"description":     "Canonical typed Scenery value",
			"one_of": []any{
				map[string]any{"type": "null"}, map[string]any{"type": "boolean"}, map[string]any{"type": "string"},
				map[string]any{"type": "array", "items": map[string]any{"$ref": "scenery.value/v1"}},
				map[string]any{"type": "object", "additional_properties": map[string]any{"$ref": "scenery.value/v1"}},
				map[string]any{"type": "object", "required": []string{"$scalar"}, "description": "Exact scalar envelope"},
				map[string]any{"type": "object", "required": []string{"$ref"}, "description": "Typed resource reference"},
			},
		}, true
	case "scenery.diagnostic/v1":
		return map[string]any{
			"schema_revision": "scenery.diagnostic/v1", "type": "object",
			"required": []string{"code", "severity", "message"},
			"properties": map[string]any{
				"code": map[string]any{"type": "string"}, "severity": map[string]any{"enum": []string{"error", "warning", "information", "hint"}},
				"message": map[string]any{"type": "string"}, "report_token": map[string]any{"type": "string", "pattern": "^rpt_[a-z2-7]+$"}, "address": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
				"range": map[string]any{"schema_revision": "scenery.source-range/v1"}, "related": map[string]any{"type": "array"}, "suggestions": map[string]any{"type": "array"}, "fixes": map[string]any{"type": "array"},
			},
		}, true
	case "scenery.semantic-operation/v1":
		return semanticOperationSchema("scenery.semantic-operation/v1", ""), true
	}
	if revision, ok := mutationSchemaRevisions[name]; ok {
		return semanticOperationSchema(revision, name), true
	}
	for operation, revision := range mutationSchemaRevisions {
		if name == revision {
			return semanticOperationSchema(revision, operation), true
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
		"path": map[string]any{"type": "string", "format": "json-pointer", "pattern": "^/spec(?:/|$)"}, "value": map[string]any{"$ref": "scenery.value/v1"},
		"precondition": map[string]any{"type": "object", "additional_properties": false, "properties": map[string]any{"exists": map[string]any{"type": "boolean"}, "absent": map[string]any{"type": "boolean"}, "equals": map[string]any{"$ref": "scenery.value/v1"}}},
	}
	required := []string{"op", "address"}
	switch operation {
	case "resource.create":
		required = append(required, "value")
		properties["value"] = map[string]any{"type": "object", "additional_properties": map[string]any{"$ref": "scenery.value/v1"}}
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
		properties["value"] = map[string]any{"type": "object", "additional_properties": map[string]any{"$ref": "scenery.value/v1"}}
		delete(properties, "path")
	case "module.upgrade":
		required = append(required, "value")
		properties["value"] = map[string]any{"type": "string", "min_length": 1, "format": "semantic-version-constraint"}
		delete(properties, "path")
	}
	result := map[string]any{
		"schema_revision": revision, "type": "object", "additional_properties": false,
		"required": required, "properties": properties,
	}
	if operation == "resource.create" {
		result["resource_kinds"] = resourceCreateSchemaRevisions()
	}
	return result
}

func allResourceSchemaRevisions() []string {
	values := make([]string, 0, len(resourceSchemas))
	for revision := range resourceSchemas {
		values = append(values, revision)
	}
	sort.Strings(values)
	return values
}

func allMutationSchemaRevisions() []string {
	values := []string{"scenery.value/v1", "scenery.diagnostic/v1", "scenery.semantic-operation/v1"}
	for _, revision := range mutationSchemaRevisions {
		values = append(values, revision)
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
