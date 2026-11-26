// Package openapiprojection projects canonical Scenery application graphs into
// deterministic, brand-neutral OpenAPI 3.1.1 documents.
package openapiprojection

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/graph"
)

const (
	// OpenAPIVersion is the OpenAPI specification version emitted by this package.
	OpenAPIVersion = "3.1.1"
	// JSONSchemaDialect is the JSON Schema dialect referenced by emitted OpenAPI 3.1.1 documents.
	JSONSchemaDialect = "https://json-schema.org/draft/2020-12/schema"
)

type schemaBuilder struct {
	resources  map[string]graph.Resource
	schemas    map[string]map[string]any
	namesByRef map[string]string
	usedNames  map[string]string
}

func newSchemaBuilder(resources map[string]graph.Resource) *schemaBuilder {
	return &schemaBuilder{
		resources:  resources,
		schemas:    map[string]map[string]any{},
		namesByRef: map[string]string{},
		usedNames:  map[string]string{},
	}
}

// schemaForType returns a JSON Schema 2020-12 object for any Scenery type value
// (scalar, standard type, wrapper expression, or named record/enum/union).
func (b *schemaBuilder) schemaForType(value any, module string) (map[string]any, error) {
	expression := strings.TrimSpace(cp.TypeExpression(value))
	return b.schemaForExpression(expression, module)
}

func (b *schemaBuilder) schemaForExpression(expression, module string) (map[string]any, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return map[string]any{}, nil
	}
	if open := strings.IndexByte(expression, '('); open > 0 && strings.HasSuffix(expression, ")") {
		wrapper := expression[:open]
		args := splitTypeArgs(expression[open+1 : len(expression)-1])
		switch wrapper {
		case "optional":
			if len(args) != 1 {
				return nil, fmt.Errorf("invalid optional type %q", expression)
			}
			return b.schemaForExpression(args[0], module)
		case "nullable":
			if len(args) != 1 {
				return nil, fmt.Errorf("invalid nullable type %q", expression)
			}
			inner, err := b.schemaForExpression(args[0], module)
			if err != nil {
				return nil, err
			}
			return makeNullableSchema(inner), nil
		case "list", "set":
			if len(args) != 1 {
				return nil, fmt.Errorf("invalid %s type %q", wrapper, expression)
			}
			items, err := b.schemaForExpression(args[0], module)
			if err != nil {
				return nil, err
			}
			schema := map[string]any{"type": "array", "items": items}
			if wrapper == "set" {
				schema["uniqueItems"] = true
			}
			return schema, nil
		case "map":
			if len(args) != 1 {
				return nil, fmt.Errorf("invalid map type %q", expression)
			}
			additional, err := b.schemaForExpression(args[0], module)
			if err != nil {
				return nil, err
			}
			return map[string]any{"type": "object", "additionalProperties": additional}, nil
		case "tuple":
			prefix := make([]any, 0, len(args))
			for _, arg := range args {
				item, err := b.schemaForExpression(arg, module)
				if err != nil {
					return nil, err
				}
				prefix = append(prefix, item)
			}
			return map[string]any{
				"type":        "array",
				"prefixItems": prefix,
				"minItems":    len(prefix),
				"maxItems":    len(prefix),
			}, nil
		}
	}

	if expression == "std.type.problem" {
		return b.refBuiltin("Problem", problemComponentSchema), nil
	}
	if expression == "std.type.execution_receipt" {
		return b.refBuiltin("ExecutionReceipt", executionReceiptComponentSchema), nil
	}
	if primitive, ok := primitiveSchema(expression); ok {
		return primitive, nil
	}

	for _, kind := range []struct {
		prefix, segment, resourceKind string
	}{
		{"record.", "/record/", "scenery.record"},
		{"enum.", "/enum/", "scenery.enum"},
		{"union.", "/union/", "scenery.union"},
	} {
		if strings.HasPrefix(expression, kind.prefix) || strings.Contains(expression, kind.segment) {
			address := resolveTypeAddress(module, expression)
			resource, ok := b.resources[address]
			if !ok || resource.Kind != kind.resourceKind {
				return nil, fmt.Errorf("referenced type %q is not present in manifest", expression)
			}
			return b.refResource(resource)
		}
	}
	return nil, fmt.Errorf("unsupported type expression %q", expression)
}

func (b *schemaBuilder) refBuiltin(name string, build func() map[string]any) map[string]any {
	key := "builtin:" + name
	if existing, ok := b.namesByRef[key]; ok {
		return map[string]any{"$ref": "#/components/schemas/" + existing}
	}
	componentName := b.reserveName(key, name, "")
	b.schemas[componentName] = build()
	return map[string]any{"$ref": "#/components/schemas/" + componentName}
}

func (b *schemaBuilder) refResource(resource graph.Resource) (map[string]any, error) {
	if existing, ok := b.namesByRef[resource.Address]; ok {
		return map[string]any{"$ref": "#/components/schemas/" + existing}, nil
	}
	// Register before recursing so self-referential records and unions resolve
	// cleanly to component $ref pointers without infinite recursion.
	name := b.reserveName(resource.Address, cp.GoName(resource.Name), cp.GoName(resource.Module))

	var (
		schema map[string]any
		err    error
	)
	switch resource.Kind {
	case "scenery.record":
		schema, err = b.buildRecordObject(resource, nil)
	case "scenery.enum":
		schema = buildEnumSchema(resource)
	case "scenery.union":
		schema, err = b.buildUnionSchema(resource)
	default:
		return nil, fmt.Errorf("unsupported schema resource kind %q", resource.Kind)
	}
	if err != nil {
		return nil, err
	}
	if desc := sanitizeNeutralText(cp.StringValue(resource.Spec["description"])); desc != "" {
		schema["description"] = desc
	}
	if resource.Spec["deprecated"] == true {
		schema["deprecated"] = true
	}
	b.schemas[name] = schema
	return map[string]any{"$ref": "#/components/schemas/" + name}, nil
}

func (b *schemaBuilder) reserveName(key, base, modulePrefix string) string {
	if existing, ok := b.namesByRef[key]; ok {
		return existing
	}
	candidate := neutralSchemaIdent(base)
	if candidate == "" {
		candidate = "Type"
	}
	if owner, taken := b.usedNames[candidate]; taken && owner != key {
		prefixed := neutralSchemaIdent(modulePrefix) + candidate
		if prefixed != candidate {
			if owner2, taken2 := b.usedNames[prefixed]; !taken2 || owner2 == key {
				candidate = prefixed
			}
		}
	}
	final := candidate
	for suffix := 2; ; suffix++ {
		if owner, taken := b.usedNames[final]; !taken || owner == key {
			break
		}
		final = fmt.Sprintf("%s%d", candidate, suffix)
	}
	b.namesByRef[key] = final
	b.usedNames[final] = key
	return final
}

// buildRecordObject builds an inline object schema for a record resource.
// When selectedFields is non-nil, only fields whose semantic name is in
// selectedFields are included (used for HTTP body include/except projections).
func (b *schemaBuilder) buildRecordObject(record graph.Resource, selectedFields map[string]bool) (map[string]any, error) {
	fields := cp.NamedChildren(record.Spec, "field")
	sort.SliceStable(fields, func(i, j int) bool {
		return cp.WireName(fields[i], cp.StringValue(fields[i]["name"])) < cp.WireName(fields[j], cp.StringValue(fields[j]["name"]))
	})
	properties := map[string]any{}
	var required []string
	for _, field := range fields {
		semanticName := cp.StringValue(field["name"])
		if selectedFields != nil && !selectedFields[semanticName] {
			continue
		}
		wire := cp.WireName(field, semanticName)
		if wire == "" {
			return nil, fmt.Errorf("record %s has a field without a name", record.Address)
		}
		fieldSchema, err := b.schemaForType(field["type"], record.Module)
		if err != nil {
			return nil, err
		}
		applyFieldMetadata(fieldSchema, field)
		properties[wire] = fieldSchema
		if !cp.IsOptionalType(field["type"]) {
			required = append(required, wire)
		}
	}
	result := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if cp.StringValue(record.Spec["unknown_fields"]) == "reject" {
		result["additionalProperties"] = false
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result, nil
}

func buildEnumSchema(resource graph.Resource) map[string]any {
	values := cp.NamedChildren(resource.Spec, "value")
	sort.SliceStable(values, func(i, j int) bool {
		return cp.StringValue(values[i]["name"]) < cp.StringValue(values[j]["name"])
	})
	schema := map[string]any{"type": "string"}
	if resource.Spec["open"] != true {
		wires := make([]string, 0, len(values))
		for _, item := range values {
			wires = append(wires, cp.WireName(item, cp.StringValue(item["name"])))
		}
		schema["enum"] = wires
	}
	return schema
}

// buildUnionSchema projects Scenery's flattened tagged-union wire representation
// into an OpenAPI 3.1 oneOf schema with a discriminator object.
func (b *schemaBuilder) buildUnionSchema(resource graph.Resource) (map[string]any, error) {
	discriminator := cp.StringValue(resource.Spec["discriminator"])
	if discriminator == "" {
		return nil, fmt.Errorf("union %s has no discriminator", resource.Address)
	}
	variants := cp.NamedChildren(resource.Spec, "variant")
	sort.SliceStable(variants, func(i, j int) bool {
		return cp.StringValue(variants[i]["name"]) < cp.StringValue(variants[j]["name"])
	})
	oneOf := make([]any, 0, len(variants)+1)
	tags := make([]string, 0, len(variants))
	for _, variant := range variants {
		tag := cp.WireName(variant, cp.StringValue(variant["name"]))
		tags = append(tags, tag)

		variantExpr := strings.TrimSpace(cp.TypeExpression(variant["type"]))
		var payload map[string]any
		if strings.HasPrefix(variantExpr, "record.") || strings.Contains(variantExpr, "/record/") {
			address := resolveTypeAddress(resource.Module, variantExpr)
			record, ok := b.resources[address]
			if !ok || record.Kind != "scenery.record" {
				return nil, fmt.Errorf("union %s variant %s record %q not found", resource.Address, tag, variantExpr)
			}
			var err error
			payload, err = b.buildRecordObject(record, nil)
			if err != nil {
				return nil, err
			}
		} else if variantExpr == "std.type.unit" {
			payload = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			}
		} else {
			built, err := b.schemaForExpression(variantExpr, resource.Module)
			if err != nil {
				return nil, err
			}
			payload = built
		}

		props := map[string]any{}
		if existing, ok := payload["properties"].(map[string]any); ok {
			maps.Copy(props, existing)
		}
		props[discriminator] = map[string]any{"type": "string", "const": tag}
		req := []string{discriminator}
		if existing, ok := payload["required"].([]string); ok {
			req = append(req, existing...)
		}
		sort.Strings(req)

		flattened := maps.Clone(payload)
		flattened["type"] = "object"
		flattened["properties"] = props
		flattened["required"] = req
		if desc := sanitizeNeutralText(cp.StringValue(variant["description"])); desc != "" {
			flattened["description"] = desc
		}
		oneOf = append(oneOf, flattened)
	}
	if resource.Spec["open"] == true {
		oneOf = append(oneOf, map[string]any{
			"type": "object",
			"properties": map[string]any{
				discriminator: map[string]any{
					"type": "string",
					"not":  map[string]any{"enum": tags},
				},
			},
			"required": []string{discriminator},
		})
	}
	return map[string]any{
		"oneOf":         oneOf,
		"discriminator": map[string]any{"propertyName": discriminator},
	}, nil
}

// primitiveSchema maps Scenery primitive scalars to brand-neutral JSON Schema
// 2020-12 definitions matching the exact HTTP/JSON wire representation.
func primitiveSchema(expression string) (map[string]any, bool) {
	switch expression {
	case "std.type.unit":
		return map[string]any{"type": "object", "additionalProperties": false, "maxProperties": 0}, true
	case "bool":
		return map[string]any{"type": "boolean"}, true
	case "int32":
		return map[string]any{"type": "integer", "format": "int32", "minimum": int64(math.MinInt32), "maximum": int64(math.MaxInt32)}, true
	case "uint32":
		return map[string]any{"type": "integer", "format": "uint32", "minimum": 0, "maximum": uint64(math.MaxUint32)}, true
	case "int64":
		return map[string]any{"type": "string", "format": "int64", "pattern": "^-?(0|[1-9][0-9]*)$"}, true
	case "uint64":
		return map[string]any{"type": "string", "format": "uint64", "pattern": "^(0|[1-9][0-9]*)$"}, true
	case "int":
		return map[string]any{"type": "string", "pattern": "^-?(0|[1-9][0-9]*)$"}, true
	case "size":
		return map[string]any{"type": "string", "pattern": "^(0|[1-9][0-9]*)$"}, true
	case "decimal":
		return map[string]any{"type": "string", "format": "decimal", "pattern": `^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`}, true
	case "float32":
		return map[string]any{"type": "number", "format": "float"}, true
	case "float64":
		return map[string]any{"type": "number", "format": "double"}, true
	case "string":
		return map[string]any{"type": "string"}, true
	case "bytes":
		return map[string]any{"type": "string", "contentEncoding": "base64"}, true
	case "uuid":
		return map[string]any{"type": "string", "format": "uuid"}, true
	case "date":
		return map[string]any{"type": "string", "format": "date"}, true
	case "datetime":
		return map[string]any{"type": "string", "format": "date-time"}, true
	case "duration":
		return map[string]any{"type": "string", "format": "duration"}, true
	case "url":
		return map[string]any{"type": "string", "format": "uri"}, true
	case "relative_path":
		return map[string]any{"type": "string"}, true
	case "json":
		return map[string]any{}, true
	default:
		return nil, false
	}
}

func problemComponentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code":    map[string]any{"type": "string"},
			"message": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string"},
		},
		"required":             []string{"code", "message"},
		"additionalProperties": false,
	}
}

func executionReceiptComponentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"durable_identity":  map[string]any{"type": "string"},
			"execution_id":      map[string]any{"type": "string"},
			"accepted_revision": map[string]any{"type": "string"},
			"status_url":        map[string]any{"type": "string", "format": "uri"},
		},
		"required":             []string{"durable_identity", "execution_id", "accepted_revision"},
		"additionalProperties": false,
	}
}

func makeNullableSchema(inner map[string]any) map[string]any {
	if _, hasRef := inner["$ref"]; !hasRef {
		if _, hasOneOf := inner["oneOf"]; !hasOneOf {
			if _, hasAnyOf := inner["anyOf"]; !hasAnyOf {
				if kind, ok := inner["type"].(string); ok {
					cloned := maps.Clone(inner)
					cloned["type"] = []string{kind, "null"}
					return cloned
				}
			}
		}
	}
	return map[string]any{
		"anyOf": []any{inner, map[string]any{"type": "null"}},
	}
}

func applyFieldMetadata(schema map[string]any, field map[string]any) {
	if schema == nil {
		return
	}
	numericTarget := schemaIsNumeric(schema)
	if numericTarget {
		for _, pair := range []struct{ schemaKey, fieldKey string }{
			{"minimum", "minimum"},
			{"maximum", "maximum"},
		} {
			if value, ok := field[pair.fieldKey]; ok {
				if converted, ok := scalarJSONValue(value); ok {
					schema[pair.schemaKey] = converted
				}
			}
		}
	}
	for _, pair := range []struct{ schemaKey, fieldKey string }{
		{"minLength", "min_length"},
		{"maxLength", "max_length"},
		{"minItems", "min_items"},
		{"maxItems", "max_items"},
	} {
		if value, ok := field[pair.fieldKey]; ok {
			if converted, ok := scalarJSONValue(value); ok {
				schema[pair.schemaKey] = converted
			}
		}
	}
	if pattern := cp.StringValue(field["pattern"]); pattern != "" {
		schema["pattern"] = pattern
	}
	if format := cp.StringValue(field["format"]); format != "" && !strings.Contains(strings.ToLower(format), "scenery") {
		schema["format"] = format
	}
	if value, ok := field["unique_items"]; ok {
		if b, isBool := value.(bool); isBool {
			schema["uniqueItems"] = b
		}
	}
	if desc := sanitizeNeutralText(cp.StringValue(field["description"])); desc != "" {
		schema["description"] = desc
	}
	if field["deprecated"] == true {
		schema["deprecated"] = true
	}
	if def, exists := field["default"]; exists && def != nil {
		schema["default"] = defaultWireValue(def, field["type"])
	}
	if examples, ok := field["examples"].([]any); ok && len(examples) > 0 {
		schema["examples"] = examples
	}
}

func schemaIsNumeric(schema map[string]any) bool {
	switch t := schema["type"].(type) {
	case string:
		return t == "integer" || t == "number"
	case []string:
		for _, item := range t {
			if item == "integer" || item == "number" {
				return true
			}
		}
	}
	return false
}

func scalarJSONValue(value any) (any, bool) {
	if scalar, ok := value.(map[string]any); ok {
		switch cp.StringValue(scalar["$scalar"]) {
		case "int":
			if parsed, err := strconv.ParseInt(cp.StringValue(scalar["value"]), 10, 64); err == nil {
				return parsed, true
			}
		case "size":
			if parsed, err := strconv.ParseInt(cp.StringValue(scalar["bytes"]), 10, 64); err == nil {
				return parsed, true
			}
		case "duration":
			if parsed, err := strconv.ParseInt(cp.StringValue(scalar["nanoseconds"]), 10, 64); err == nil {
				return parsed, true
			}
		case "decimal":
			if text := cp.StringValue(scalar); text != "" {
				if parsed, err := strconv.ParseFloat(text, 64); err == nil {
					return parsed, true
				}
			}
		}
		return nil, false
	}
	switch typed := value.(type) {
	case int, int64, uint64, float64, json.Number:
		return typed, true
	}
	return nil, false
}

func defaultWireValue(value any, fieldType any) any {
	baseType := unwrapOptionalNullable(cp.TypeExpression(fieldType))
	if scalar, ok := value.(map[string]any); ok && cp.StringValue(scalar["$scalar"]) != "" {
		switch baseType {
		case "int32", "uint32":
			if parsed, err := strconv.ParseInt(cp.StringValue(scalar["value"]), 10, 64); err == nil {
				return parsed
			}
		case "float32", "float64":
			if parsed, err := strconv.ParseFloat(cp.StringValue(scalar), 64); err == nil {
				return parsed
			}
		default:
			return cp.StringValue(scalar)
		}
	}
	return value
}

func unwrapOptionalNullable(expression string) string {
	expression = strings.TrimSpace(expression)
	for {
		switch {
		case strings.HasPrefix(expression, "optional(") && strings.HasSuffix(expression, ")"):
			expression = strings.TrimSpace(expression[len("optional(") : len(expression)-1])
		case strings.HasPrefix(expression, "nullable(") && strings.HasSuffix(expression, ")"):
			expression = strings.TrimSpace(expression[len("nullable(") : len(expression)-1])
		default:
			return expression
		}
	}
}

func resolveTypeAddress(module, expression string) string {
	if strings.Contains(expression, "/") {
		return expression
	}
	parts := strings.Split(expression, ".")
	if len(parts) != 2 {
		return expression
	}
	return graph.ResourceAddress(module, parts[0], parts[1])
}

func splitTypeArgs(raw string) []string {
	var args []string
	depth, start := 0, 0
	for index, r := range raw {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(raw[start:index]))
				start = index + 1
			}
		}
	}
	if tail := strings.TrimSpace(raw[start:]); tail != "" {
		args = append(args, tail)
	}
	return args
}

func neutralSchemaIdent(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return stripFrameworkBrand(b.String())
}

func sanitizeNeutralText(value string) string {
	return strings.TrimSpace(value)
}

func stripFrameworkBrand(value string) string {
	lower := strings.ToLower(value)
	const brand = "scenery"
	for {
		idx := strings.Index(lower, brand)
		if idx < 0 {
			return value
		}
		value = value[:idx] + value[idx+len(brand):]
		lower = lower[:idx] + lower[idx+len(brand):]
	}
}
