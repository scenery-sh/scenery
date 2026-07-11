package vnext

import (
	"mime"
	"sort"
	"strings"
)

// httpResponseDecodersDisjoint proves that two same-status completion mappings
// cannot both accept the same observable response. A failure to prove
// disjointness is intentionally treated as overlap by the caller.
func httpResponseDecodersDisjoint(resources map[string]Resource, operation Resource, left, right map[string]any) bool {
	leftBody, _ := left["body"].(map[string]any)
	rightBody, _ := right["body"].(map[string]any)
	if leftBody == nil || rightBody == nil {
		return false
	}
	if !httpResponseMediaTypesOverlap(leftBody, rightBody) {
		return true
	}
	leftCodec := stringValue(leftBody["codec"])
	rightCodec := stringValue(rightBody["codec"])
	if httpResponseCodecFamily(leftCodec) != httpResponseCodecFamily(rightCodec) {
		return false
	}
	if httpResponseCodecFamily(leftCodec) == "bytes" {
		return false
	}
	leftType, _, leftErr := httpOutcomeMappedValueType(resources, operation, refOrString(left["when"]), refOrString(leftBody["from"]))
	rightType, _, rightErr := httpOutcomeMappedValueType(resources, operation, refOrString(right["when"]), refOrString(rightBody["from"]))
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftShape := httpObservableTypeShape(tsDescriptor(leftType, operation.Module), resources, map[string]bool{})
	rightShape := httpObservableTypeShape(tsDescriptor(rightType, operation.Module), resources, map[string]bool{})
	return !httpObservableTypeShapesOverlap(leftShape, rightShape)
}

func httpResponseMediaTypesOverlap(left, right map[string]any) bool {
	leftTypes := httpResponseMediaTypes(left)
	rightTypes := httpResponseMediaTypes(right)
	for _, leftType := range leftTypes {
		for _, rightType := range rightTypes {
			if leftType == rightType {
				return true
			}
		}
	}
	return false
}

func httpResponseMediaTypes(body map[string]any) []string {
	values := literalStringListFromValue(body["produced_media_types"])
	if len(values) == 0 {
		values = []string{defaultHTTPMediaType(stringValue(body["codec"]))}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		mediaType, parameters, err := mime.ParseMediaType(value)
		if err != nil {
			result = append(result, strings.ToLower(value))
			continue
		}
		result = append(result, mime.FormatMediaType(strings.ToLower(mediaType), parameters))
	}
	return result
}

func httpResponseCodecFamily(codec string) string {
	switch codec {
	case "json", "problem_json":
		return "json"
	case "text":
		return "text"
	case "bytes":
		return "bytes"
	default:
		return "unknown"
	}
}

func httpObservableTypeShape(value any, resources map[string]Resource, resolving map[string]bool) map[string]any {
	descriptor, _ := value.(map[string]any)
	kind := stringValue(descriptor["kind"])
	switch kind {
	case "named":
		name := stringValue(descriptor["name"])
		if resolving[name] {
			return map[string]any{"kind": "recursive"}
		}
		resource, ok := resources[name]
		if !ok {
			return map[string]any{"kind": "unknown"}
		}
		next := cloneBoolMap(resolving)
		next[name] = true
		return httpObservableTypeShape(tsNamedDescriptor(resource), resources, next)
	case "optional", "nullable", "list", "set", "map":
		return map[string]any{"kind": kind, "value": httpObservableTypeShape(descriptor["value"], resources, resolving)}
	case "tuple":
		values := make([]any, 0, len(anyList(descriptor["values"])))
		for _, item := range anyList(descriptor["values"]) {
			values = append(values, httpObservableTypeShape(item, resources, resolving))
		}
		return map[string]any{"kind": kind, "values": values}
	case "record":
		fields := make([]any, 0, len(anyList(descriptor["fields"])))
		for _, item := range anyList(descriptor["fields"]) {
			field, _ := item.(map[string]any)
			fields = append(fields, map[string]any{
				"wire":     stringValue(field["wire"]),
				"optional": field["optional"] == true,
				"value":    httpObservableTypeShape(field["value"], resources, resolving),
			})
		}
		sort.Slice(fields, func(i, j int) bool {
			left, _ := fields[i].(map[string]any)
			right, _ := fields[j].(map[string]any)
			return stringValue(left["wire"]) < stringValue(right["wire"])
		})
		return map[string]any{"kind": kind, "fields": fields, "preserveUnknown": descriptor["preserveUnknown"] == true}
	case "enum":
		values := httpStringValues(descriptor["values"])
		sort.Strings(values)
		return map[string]any{"kind": kind, "values": values, "open": descriptor["open"] == true}
	case "union":
		variants := map[string]any{}
		for name, item := range httpMapValue(descriptor["variants"]) {
			variants[name] = httpObservableTypeShape(item, resources, resolving)
		}
		return map[string]any{"kind": kind, "discriminator": stringValue(descriptor["discriminator"]), "variants": variants, "open": descriptor["open"] == true}
	case "primitive":
		return httpObservablePrimitiveShape(stringValue(descriptor["name"]))
	default:
		return map[string]any{"kind": "unknown"}
	}
}

func httpObservablePrimitiveShape(name string) map[string]any {
	switch name {
	case "json":
		return map[string]any{"kind": "any"}
	case "bool":
		return map[string]any{"kind": "scalar", "class": "bool"}
	case "int32", "uint32", "float32", "float64":
		return map[string]any{"kind": "scalar", "class": "number"}
	case "int", "int64", "uint64", "size", "bytes", "decimal", "uuid", "date", "datetime", "duration", "url", "relative_path", "string":
		return map[string]any{"kind": "scalar", "class": "string"}
	case "unit":
		return map[string]any{"kind": "record", "fields": []any{}, "preserveUnknown": false}
	case "problem":
		return httpBuiltinRecordShape([]string{"code", "message"}, []string{"path"})
	case "execution_receipt":
		return httpBuiltinRecordShape([]string{"accepted_revision", "durable_identity", "execution_id"}, []string{"status_url"})
	default:
		return map[string]any{"kind": "unknown"}
	}
}

func httpBuiltinRecordShape(required, optional []string) map[string]any {
	fields := make([]any, 0, len(required)+len(optional))
	for _, name := range required {
		fields = append(fields, map[string]any{"wire": name, "optional": false, "value": map[string]any{"kind": "scalar", "class": "string"}})
	}
	for _, name := range optional {
		fields = append(fields, map[string]any{"wire": name, "optional": true, "value": map[string]any{"kind": "scalar", "class": "string"}})
	}
	return map[string]any{"kind": "record", "fields": fields, "preserveUnknown": true}
}

func httpObservableTypeShapesOverlap(left, right map[string]any) bool {
	leftKind := stringValue(left["kind"])
	rightKind := stringValue(right["kind"])
	if leftKind == "unknown" || rightKind == "unknown" || leftKind == "recursive" || rightKind == "recursive" || leftKind == "any" || rightKind == "any" {
		return true
	}
	if leftKind == "optional" {
		return httpObservableTypeShapesOverlap(httpMapValue(left["value"]), right)
	}
	if rightKind == "optional" {
		return httpObservableTypeShapesOverlap(left, httpMapValue(right["value"]))
	}
	if leftKind == "nullable" && rightKind == "nullable" {
		return true
	}
	if leftKind == "nullable" {
		return httpObservableTypeShapesOverlap(httpMapValue(left["value"]), right)
	}
	if rightKind == "nullable" {
		return httpObservableTypeShapesOverlap(left, httpMapValue(right["value"]))
	}
	if leftKind == "scalar" && rightKind == "scalar" {
		return stringValue(left["class"]) == stringValue(right["class"])
	}
	if leftKind == "enum" && rightKind == "enum" {
		if left["open"] == true || right["open"] == true {
			return true
		}
		seen := map[string]bool{}
		for _, value := range httpStringValues(left["values"]) {
			seen[value] = true
		}
		for _, value := range httpStringValues(right["values"]) {
			if seen[value] {
				return true
			}
		}
		return false
	}
	if leftKind == "enum" && rightKind == "scalar" {
		return stringValue(right["class"]) == "string"
	}
	if rightKind == "enum" && leftKind == "scalar" {
		return stringValue(left["class"]) == "string"
	}
	if leftKind == "record" && rightKind == "record" {
		return httpObservableRecordsOverlap(left, right)
	}
	if leftKind == "tuple" && rightKind == "tuple" {
		leftValues := anyList(left["values"])
		rightValues := anyList(right["values"])
		if len(leftValues) != len(rightValues) {
			return false
		}
		for index := range leftValues {
			if !httpObservableTypeShapesOverlap(httpMapValue(leftValues[index]), httpMapValue(rightValues[index])) {
				return false
			}
		}
		return true
	}
	if httpObservableArrayKind(leftKind) && httpObservableArrayKind(rightKind) {
		return true
	}
	if httpObservableObjectKind(leftKind) && httpObservableObjectKind(rightKind) {
		return true
	}
	return false
}

func httpObservableRecordsOverlap(left, right map[string]any) bool {
	leftFields := httpObservableRecordFields(left)
	rightFields := httpObservableRecordFields(right)
	leftOpen := left["preserveUnknown"] == true
	rightOpen := right["preserveUnknown"] == true
	for name, field := range leftFields {
		other, exists := rightFields[name]
		if !exists {
			if field["optional"] != true && !rightOpen {
				return false
			}
			continue
		}
		if (field["optional"] != true || other["optional"] != true) && !httpObservableTypeShapesOverlap(httpMapValue(field["value"]), httpMapValue(other["value"])) {
			return false
		}
	}
	for name, field := range rightFields {
		if _, exists := leftFields[name]; !exists && field["optional"] != true && !leftOpen {
			return false
		}
	}
	return true
}

func httpObservableRecordFields(record map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, item := range anyList(record["fields"]) {
		field := httpMapValue(item)
		result[stringValue(field["wire"])] = field
	}
	return result
}

func httpObservableArrayKind(kind string) bool {
	return kind == "list" || kind == "set" || kind == "tuple"
}

func httpObservableObjectKind(kind string) bool {
	return kind == "record" || kind == "map" || kind == "union"
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func httpMapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func httpStringValues(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
