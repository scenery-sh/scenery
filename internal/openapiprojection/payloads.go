package openapiprojection

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/graph"
)

func buildParameters(
	httpSpec map[string]any,
	operation graph.Resource,
	shape cp.OperationInputShape,
	builder *schemaBuilder,
) ([]map[string]any, error) {
	var params []map[string]any
	addMapped := func(childKind, location string, alwaysRequired, supportsEncoding, isPathTail bool) error {
		children := cp.NamedChildren(httpSpec, childKind)
		for _, mapping := range children {
			name := cp.StringValue(mapping["name"])
			if name == "" {
				continue
			}
			field, ok := resolveInputField(operation, shape, cp.RefString(mapping["to"]))
			if !ok {
				return fmt.Errorf("%s mapping %q references unknown input field %q", childKind, name, cp.RefString(mapping["to"]))
			}
			param := map[string]any{
				"name":     name,
				"in":       location,
				"required": alwaysRequired || (!field.Optional && !field.HasDefault),
			}
			fieldSchema, err := builder.schemaForType(field.Type, operation.Module)
			if err != nil {
				return err
			}
			if rawField := recordFieldSpec(shape.Record, field.Name); rawField != nil {
				applyFieldMetadata(fieldSchema, rawField)
				if desc := cp.StringValue(rawField["description"]); desc != "" {
					param["description"] = desc
				}
				if rawField["deprecated"] == true {
					param["deprecated"] = true
				}
			}
			if isPathTail {
				tailNote := "Captures the remaining slash-separated path tail."
				if existing, _ := param["description"].(string); existing != "" {
					param["description"] = existing + " " + tailNote
				} else {
					param["description"] = tailNote
				}
			}
			encoding := cp.StringValue(mapping["encoding"])
			if supportsEncoding && encoding == "json" {
				param["content"] = map[string]any{
					"application/json": map[string]any{"schema": fieldSchema},
				}
			} else {
				param["schema"] = fieldSchema
				if location == "query" {
					switch encoding {
					case "comma":
						param["style"] = "form"
						param["explode"] = false
					case "repeated":
						param["style"] = "form"
						param["explode"] = true
					}
				}
			}
			params = append(params, param)
		}
		return nil
	}

	if err := addMapped("path_parameter", "path", true, false, false); err != nil {
		return nil, err
	}
	if err := addMapped("path_tail", "path", true, false, true); err != nil {
		return nil, err
	}
	if err := addMapped("query_parameter", "query", false, true, false); err != nil {
		return nil, err
	}
	if err := addMapped("header", "header", false, true, false); err != nil {
		return nil, err
	}
	if err := addMapped("cookie", "cookie", false, false, false); err != nil {
		return nil, err
	}
	return params, nil
}

func buildRequestBody(
	httpSpec map[string]any,
	operation graph.Resource,
	shape cp.OperationInputShape,
	builder *schemaBuilder,
) (map[string]any, error) {
	body, _ := httpSpec["body"].(map[string]any)
	if body == nil {
		return nil, nil
	}
	codec := cp.StringValue(body["codec"])
	target := cp.RefString(body["to"])
	whole := target == "" || target == "operation."+operation.Name+".input"

	if codec == "multipart" {
		return buildMultipartRequestBody(body, operation, shape, builder)
	}

	var (
		schema   map[string]any
		required bool
		err      error
	)
	if !whole {
		field, ok := resolveInputField(operation, shape, target)
		if !ok {
			return nil, fmt.Errorf("body mapping references unknown input field %q", target)
		}
		required = !field.Optional && !field.HasDefault
		schema, err = builder.schemaForType(field.Type, operation.Module)
		if err != nil {
			return nil, err
		}
		if rawField := recordFieldSpec(shape.Record, field.Name); rawField != nil {
			applyFieldMetadata(schema, rawField)
		}
	} else if shape.Record != nil {
		selected, hasFilter := selectedBodyFieldSet(body, operation, shape)
		if hasFilter || codec == "form" {
			schema, err = builder.buildRecordObject(*shape.Record, selected)
			if err != nil {
				return nil, err
			}
			required = hasRequiredSelectedField(shape, selected)
		} else {
			schema, err = builder.schemaForType(operation.Spec["input"], operation.Module)
			if err != nil {
				return nil, err
			}
			required = hasRequiredSelectedField(shape, nil)
		}
	} else {
		schema, err = builder.schemaForType(operation.Spec["input"], operation.Module)
		if err != nil {
			return nil, err
		}
		required = !cp.IsOptionalType(operation.Spec["input"])
	}

	mediaTypes := cp.StringValues(body["accepted_media_types"])
	if len(mediaTypes) == 0 {
		mediaTypes = []string{defaultMediaType(codec)}
	}
	content := map[string]any{}
	for _, mediaType := range mediaTypes {
		mediaSchema := schema
		if codec == "bytes" {
			mediaSchema = map[string]any{"type": "string", "contentMediaType": mediaType}
		} else if codec == "text" && mediaSchema["type"] != "string" {
			mediaSchema = map[string]any{"type": "string"}
		}
		content[mediaType] = map[string]any{"schema": mediaSchema}
	}
	return map[string]any{
		"required": required,
		"content":  content,
	}, nil
}

func buildMultipartRequestBody(
	body map[string]any,
	operation graph.Resource,
	shape cp.OperationInputShape,
	builder *schemaBuilder,
) (map[string]any, error) {
	parts := cp.NamedChildren(body, "part")
	properties := map[string]any{}
	encoding := map[string]any{}
	var required []string

	for _, part := range parts {
		name := cp.StringValue(part["name"])
		field, ok := resolveInputField(operation, shape, cp.RefString(part["to"]))
		if !ok {
			return nil, fmt.Errorf("multipart part %q references unknown input field %q", name, cp.RefString(part["to"]))
		}
		kind := cp.StringValue(part["kind"])
		multiple := part["multiple"] == true
		mediaTypes := cp.StringValues(part["media_types"])

		var partSchema map[string]any
		switch kind {
		case "file", "bytes":
			contentType := "application/octet-stream"
			for _, candidate := range mediaTypes {
				if !strings.Contains(candidate, "*") {
					contentType = candidate
					break
				}
			}
			itemSchema := map[string]any{"type": "string", "contentMediaType": contentType}
			if multiple {
				partSchema = map[string]any{"type": "array", "items": itemSchema}
			} else {
				partSchema = itemSchema
			}
		default:
			var err error
			partSchema, err = builder.schemaForType(field.Type, operation.Module)
			if err != nil {
				return nil, err
			}
		}
		if rawField := recordFieldSpec(shape.Record, field.Name); rawField != nil {
			if desc := cp.StringValue(rawField["description"]); desc != "" {
				partSchema["description"] = desc
			}
		}
		properties[name] = partSchema
		if !field.Optional && !field.HasDefault {
			required = append(required, name)
		}
		if len(mediaTypes) > 0 {
			encoding[name] = map[string]any{"contentType": strings.Join(mediaTypes, ", ")}
		}
	}

	sort.Strings(required)
	objectSchema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		objectSchema["required"] = required
	}
	mediaObject := map[string]any{"schema": objectSchema}
	if len(encoding) > 0 {
		mediaObject["encoding"] = encoding
	}
	mediaTypes := cp.StringValues(body["accepted_media_types"])
	if len(mediaTypes) == 0 {
		mediaTypes = []string{"multipart/form-data"}
	}
	content := map[string]any{}
	for _, mediaType := range mediaTypes {
		content[mediaType] = mediaObject
	}
	return map[string]any{
		"required": len(required) > 0,
		"content":  content,
	}, nil
}

func buildResponses(
	binding graph.Resource,
	httpSpec map[string]any,
	operation graph.Resource,
	byAddress map[string]graph.Resource,
	builder *schemaBuilder,
) (map[string]any, error) {
	type statusGroup struct {
		status    int
		responses []map[string]any
	}
	groups := map[int]*statusGroup{}
	var statuses []int
	for _, resp := range cp.NamedChildren(httpSpec, "response") {
		status, ok := cp.IntegerValue(resp["status"])
		if !ok || status == 0 {
			continue
		}
		if groups[status] == nil {
			groups[status] = &statusGroup{status: status}
			statuses = append(statuses, status)
		}
		groups[status].responses = append(groups[status].responses, resp)
	}
	sort.Ints(statuses)

	result := map[string]any{}
	for _, status := range statuses {
		group := groups[status]
		mediaSchemas := map[string][]map[string]any{}
		mediaSeen := map[string]map[string]bool{}
		headers := map[string]any{}
		var cookieNames []string
		hasSSE := false

		for _, resp := range group.responses {
			when := cp.RefString(resp["when"])
			if body, _ := resp["body"].(map[string]any); body != nil {
				codec := cp.StringValue(body["codec"])
				valueType := resolveOutcomeValueType(operation, when, cp.RefString(body["from"]), byAddress)
				mediaTypes := cp.StringValues(body["produced_media_types"])
				if len(mediaTypes) == 0 {
					mediaTypes = []string{defaultMediaType(codec)}
				}
				for _, mediaType := range mediaTypes {
					var (
						schema map[string]any
						err    error
					)
					switch codec {
					case "bytes":
						schema = map[string]any{"type": "string", "contentMediaType": mediaType}
					case "sse":
						hasSSE = true
						schema, err = builder.schemaForType(valueType, operation.Module)
					default:
						schema, err = builder.schemaForType(valueType, operation.Module)
					}
					if err != nil {
						return nil, err
					}
					if mediaSeen[mediaType] == nil {
						mediaSeen[mediaType] = map[string]bool{}
					}
					sig := canonicalSignature(schema)
					if !mediaSeen[mediaType][sig] {
						mediaSeen[mediaType][sig] = true
						mediaSchemas[mediaType] = append(mediaSchemas[mediaType], schema)
					}
				}
			}

			for _, header := range cp.NamedChildren(resp, "header") {
				name := cp.StringValue(header["name"])
				if name == "" {
					continue
				}
				valueType := resolveOutcomeValueType(operation, when, cp.RefString(header["from"]), byAddress)
				schema, err := builder.schemaForType(valueType, operation.Module)
				if err != nil {
					return nil, err
				}
				headerObj := map[string]any{}
				if cp.StringValue(header["encoding"]) == "json" {
					headerObj["content"] = map[string]any{
						"application/json": map[string]any{"schema": schema},
					}
				} else {
					headerObj["schema"] = schema
				}
				if !cp.IsOptionalType(valueType) && len(group.responses) == 1 {
					headerObj["required"] = true
				}
				headers[name] = headerObj
			}

			for _, cookie := range cp.NamedChildren(resp, "cookie") {
				if name := cp.StringValue(cookie["name"]); name != "" {
					cookieNames = append(cookieNames, name)
				}
			}
		}

		if len(cookieNames) > 0 {
			sort.Strings(cookieNames)
			headers["Set-Cookie"] = map[string]any{
				"description": "Sets cookie(s): " + strings.Join(dedupeStrings(cookieNames), ", "),
				"schema":      map[string]any{"type": "string"},
			}
		}

		description := responseDescription(status, group.responses, hasSSE, cp.StringValue(binding.Spec["delivery"]))
		responseObj := map[string]any{"description": description}
		if len(headers) > 0 {
			responseObj["headers"] = headers
		}
		if len(mediaSchemas) > 0 {
			content := map[string]any{}
			mediaKeys := make([]string, 0, len(mediaSchemas))
			for mediaType := range mediaSchemas {
				mediaKeys = append(mediaKeys, mediaType)
			}
			sort.Strings(mediaKeys)
			for _, mediaType := range mediaKeys {
				list := mediaSchemas[mediaType]
				if len(list) == 1 {
					content[mediaType] = map[string]any{"schema": list[0]}
				} else {
					variants := make([]any, len(list))
					for i, item := range list {
						variants[i] = item
					}
					content[mediaType] = map[string]any{"schema": map[string]any{"oneOf": variants}}
				}
			}
			responseObj["content"] = content
		}
		result[strconv.Itoa(status)] = responseObj
	}

	if len(result) == 0 {
		result["default"] = map[string]any{"description": "HTTP response."}
	}
	return result, nil
}

func resolveInputField(operation graph.Resource, shape cp.OperationInputShape, target string) (cp.OperationInputField, bool) {
	prefix := "operation." + operation.Name + ".input."
	fieldName, ok := strings.CutPrefix(target, prefix)
	if !ok {
		return cp.OperationInputField{}, false
	}
	field, exists := shape.Fields[fieldName]
	return field, exists
}

func recordFieldSpec(record *graph.Resource, semanticName string) map[string]any {
	if record == nil {
		return nil
	}
	for _, field := range cp.NamedChildren(record.Spec, "field") {
		if cp.StringValue(field["name"]) == semanticName {
			return field
		}
	}
	return nil
}

func selectedBodyFieldSet(body map[string]any, operation graph.Resource, shape cp.OperationInputShape) (map[string]bool, bool) {
	if rawInclude, ok := body["include"].([]any); ok {
		selected := map[string]bool{}
		for _, item := range rawInclude {
			if field, found := resolveInputField(operation, shape, cp.RefString(item)); found {
				selected[field.Name] = true
			}
		}
		return selected, true
	}
	if rawExcept, ok := body["except"].([]any); ok {
		excluded := map[string]bool{}
		for _, item := range rawExcept {
			if field, found := resolveInputField(operation, shape, cp.RefString(item)); found {
				excluded[field.Name] = true
			}
		}
		selected := map[string]bool{}
		for name := range shape.Fields {
			if !excluded[name] {
				selected[name] = true
			}
		}
		return selected, true
	}
	return nil, false
}

func hasRequiredSelectedField(shape cp.OperationInputShape, selected map[string]bool) bool {
	for name, field := range shape.Fields {
		if selected != nil && !selected[name] {
			continue
		}
		if !field.Optional && !field.HasDefault {
			return true
		}
	}
	return false
}

func resolveOutcomeValueType(operation graph.Resource, when, from string, byAddress map[string]graph.Resource) any {
	rootType := outcomeRootType(operation, when)
	parts := strings.Split(from, ".")
	if len(parts) <= 2 {
		return rootType
	}
	path := parts[2:]
	switch cp.RefString(rootType) {
	case "std.type.problem":
		return map[string]any{"$ref": "string"}
	case "std.type.execution_receipt":
		if path[0] == "status_url" {
			return map[string]any{"$ref": "url"}
		}
		return map[string]any{"$ref": "string"}
	default:
		if resolved := nestedRecordFieldType(byAddress, operation.Module, rootType, path); resolved != nil {
			return resolved
		}
		return rootType
	}
}

func outcomeRootType(operation graph.Resource, when string) any {
	parts := strings.Split(when, ".")
	if len(parts) == 2 && (parts[0] == "result" || parts[0] == "error") {
		for _, child := range cp.NamedChildren(operation.Spec, parts[0]) {
			if cp.StringValue(child["name"]) == parts[1] {
				return child["type"]
			}
		}
	}
	if when == "dispatch.enqueued" {
		return map[string]any{"$ref": "std.type.execution_receipt"}
	}
	return map[string]any{"$ref": "std.type.problem"}
}

func nestedRecordFieldType(byAddress map[string]graph.Resource, module string, current any, path []string) any {
	for _, segment := range path {
		expr := unwrapOptionalNullable(cp.TypeExpression(current))
		address := resolveTypeAddress(module, expr)
		record, ok := byAddress[address]
		if !ok || record.Kind != "scenery.record" {
			return nil
		}
		module = record.Module
		found := false
		for _, field := range cp.NamedChildren(record.Spec, "field") {
			if cp.StringValue(field["name"]) == segment {
				current = field["type"]
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return current
}

func defaultMediaType(codec string) string {
	switch codec {
	case "problem_json":
		return "application/problem+json"
	case "text":
		return "text/plain"
	case "bytes":
		return "application/octet-stream"
	case "form":
		return "application/x-www-form-urlencoded"
	case "multipart":
		return "multipart/form-data"
	case "sse":
		return "text/event-stream"
	default:
		return "application/json"
	}
}

func responseDescription(status int, responses []map[string]any, hasSSE bool, delivery string) string {
	if hasSSE {
		return "Server-Sent Events stream; each event data frame carries a JSON payload matching the schema."
	}
	if delivery == "stream" && status >= 200 && status < 300 {
		return "Streaming binary response."
	}
	if text := http.StatusText(status); text != "" {
		return text
	}
	var names []string
	for _, resp := range responses {
		if name := cp.StringValue(resp["name"]); name != "" {
			names = append(names, humanizeName(name))
		}
	}
	if len(names) > 0 {
		return strings.Join(dedupeStrings(names), ", ")
	}
	return fmt.Sprintf("HTTP %d response", status)
}

func canonicalSignature(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func dedupeStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if len(out) == 0 || out[len(out)-1] != item {
			out = append(out, item)
		}
	}
	return out
}
