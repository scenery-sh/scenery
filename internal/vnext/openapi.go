package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type OpenAPIArtifact struct {
	Gateway             string `json:"gateway"`
	ContractRevision    string `json:"contract_revision"`
	HTTPSurfaceRevision string `json:"http_surface_revision"`
	OpenAPIRevision     string `json:"openapi_revision"`
	Document            []byte `json:"document"`
	Descriptor          []byte `json:"descriptor"`
}

func GenerateOpenAPIArtifact(result *Result, selector string) (OpenAPIArtifact, error) {
	if result == nil || result.Manifest == nil || !result.Valid() {
		return OpenAPIArtifact{}, fmt.Errorf("cannot generate OpenAPI from an invalid contract")
	}
	gateway, ok := selectHTTPGateway(result.Manifest.Resources, selector)
	if !ok {
		return OpenAPIArtifact{}, fmt.Errorf("HTTP gateway %q not found", selector)
	}
	httpRevision := result.HTTPSurfaceRevisions[gateway.Address]
	openAPIRevision := result.OpenAPIRevisions[gateway.Address]
	document := renderOpenAPIDocument(result.Manifest, gateway, httpRevision, openAPIRevision)
	documentBytes, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return OpenAPIArtifact{}, err
	}
	documentBytes = append(documentBytes, '\n')
	digest := sha256.Sum256(documentBytes)
	descriptor := map[string]any{
		"api_version": "scenery.openapi-generated/v1", "gateway": gateway.Address,
		"contract_revision": result.Manifest.ContractRevision, "http_surface_revision": httpRevision, "openapi_revision": openAPIRevision,
		"profile": openAPIGeneratorProfile, "openapi_version": openAPIVersion,
		"content_digest": "sha256:" + hex.EncodeToString(digest[:]), "generator": "scenery.vnext.openapi/v1",
	}
	if bindingsUseHTTPPathTail(httpBindingsForGateway(result.Manifest.Resources, gateway)) {
		descriptor["extension_profiles"] = []string{HTTPPathTailProfile}
	}
	descriptorBytes, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return OpenAPIArtifact{}, err
	}
	return OpenAPIArtifact{Gateway: gateway.Address, ContractRevision: result.Manifest.ContractRevision, HTTPSurfaceRevision: httpRevision, OpenAPIRevision: openAPIRevision, Document: documentBytes, Descriptor: append(descriptorBytes, '\n')}, nil
}

func selectHTTPGateway(resources []Resource, selector string) (Resource, bool) {
	selector = strings.TrimPrefix(selector, "http_gateway.")
	for _, resource := range resources {
		if resource.Kind == "scenery.http-gateway/v1" && (selector == "" || selector == resource.Name || selector == resource.Address) {
			return resource, true
		}
	}
	return Resource{}, false
}

func renderOpenAPIDocument(manifest *Manifest, gateway Resource, httpRevision, openAPIRevision string) map[string]any {
	resources := manifest.Resources
	bindings := httpBindingsForGateway(resources, gateway)
	reachable := reachableResources(resources, bindings)
	byAddress := resourcesByAddress(manifest)
	components := map[string]any{"schemas": openAPIComponents(reachable, resources), "securitySchemes": map[string]any{"sceneryAuth": map[string]any{"type": "http", "scheme": "bearer"}}}
	paths := map[string]any{}
	for _, binding := range bindings {
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		operation := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
		pathValue := joinHTTPPath(stringValue(gateway.Spec["base_path"]), stringValue(httpSpec["path"]))
		pathItem, _ := paths[pathValue].(map[string]any)
		if pathItem == nil {
			pathItem = map[string]any{}
			paths[pathValue] = pathItem
		}
		pathItem[strings.ToLower(stringValue(httpSpec["method"]))] = renderOpenAPIOperation(binding, operation, httpSpec, resources)
	}
	version := manifest.Application.Version
	if version == "" {
		version = "0.0.0"
	}
	return map[string]any{
		"openapi": openAPIVersion,
		"info":    map[string]any{"title": manifest.Application.Name, "version": version},
		"paths":   paths, "components": components,
		"x-scenery-contract-revision":     manifest.ContractRevision,
		"x-scenery-http-surface-revision": httpRevision,
		"x-scenery-openapi-revision":      openAPIRevision,
		"x-scenery-gateway":               gateway.Address,
	}
}

func renderOpenAPIOperation(binding, operation Resource, httpSpec map[string]any, resources []Resource) map[string]any {
	value := map[string]any{
		"operationId":         semanticPathName(binding.Module + "_" + binding.Name),
		"responses":           renderOpenAPIResponses(operation, httpSpec, resources),
		"x-scenery-binding":   binding.Address,
		"x-scenery-guarantee": stringValue(httpSpec["guarantee"]),
		"x-scenery-delivery":  stringValue(binding.Spec["delivery"]),
		"x-scenery-limits":    map[string]any{"request": httpSpec["request_limit"], "response": httpSpec["response_limit"], "timeouts": httpSpec["timeouts"]},
	}
	if refOrString(binding.Spec["authentication"]) == "std.authentication.none" {
		value["security"] = []any{}
	} else {
		value["security"] = []any{map[string]any{"sceneryAuth": []any{}}}
	}
	var parameters []any
	for _, source := range []struct{ child, location string }{{"path_parameter", "path"}, {"query_parameter", "query"}, {"header", "header"}, {"cookie", "cookie"}} {
		for _, mapping := range namedChildren(httpSpec, source.child) {
			parameter := map[string]any{"name": stringValue(mapping["name"]), "in": source.location, "required": source.location == "path" || mapping["optional"] != true}
			parameter["schema"] = openAPISchemaForMapping(mapping, operation, resources)
			if encoding := stringValue(mapping["encoding"]); encoding != "" {
				parameter["x-scenery-encoding"] = encoding
			}
			parameters = append(parameters, parameter)
		}
	}
	if len(parameters) > 0 {
		value["parameters"] = parameters
	}
	if tails := namedChildren(httpSpec, "path_tail"); len(tails) == 1 {
		tail := tails[0]
		value["x-scenery-path-tail"] = map[string]any{
			"template":         stringValue(httpSpec["path"]),
			"name":             stringValue(tail["name"]),
			"target":           refOrString(tail["to"]),
			"target_type":      stringValue(tail["target_type"]),
			"cardinality":      "zero_or_more",
			"minimum_segments": tail["minimum_segments"],
			"empty_capture":    stringValue(tail["empty_capture"]),
			"decoding":         stringValue(tail["decoding"]),
			"segment_encoding": "rfc3986_independent",
			"trailing_slash":   "no_match",
		}
	}
	if body, _ := httpSpec["body"].(map[string]any); body != nil {
		mediaTypes := literalStringListFromValue(body["accepted_media_types"])
		if len(mediaTypes) == 0 {
			mediaTypes = []string{defaultHTTPMediaType(stringValue(body["codec"]))}
		}
		content := map[string]any{}
		for _, mediaType := range mediaTypes {
			content[mediaType] = map[string]any{"schema": openAPISchemaForType(operation.Spec["input"], operation.Module, resources)}
		}
		value["requestBody"] = map[string]any{"required": true, "content": content, "x-scenery-codec": stringValue(body["codec"])}
	}
	return value
}

func renderOpenAPIResponses(operation Resource, httpSpec map[string]any, resources []Resource) map[string]any {
	responses := map[string]any{}
	for _, response := range namedChildren(httpSpec, "response") {
		status := stringValue(response["status"])
		when := refOrString(response["when"])
		entry, _ := responses[status].(map[string]any)
		if entry == nil {
			entry = map[string]any{"description": when, "x-scenery-outcomes": []any{when}}
			responses[status] = entry
		} else {
			entry["x-scenery-outcomes"] = append(entry["x-scenery-outcomes"].([]any), when)
		}
		if body, _ := response["body"].(map[string]any); body != nil {
			mediaTypes := literalStringListFromValue(body["produced_media_types"])
			if len(mediaTypes) == 0 {
				mediaTypes = []string{defaultHTTPMediaType(stringValue(body["codec"]))}
			}
			content, _ := entry["content"].(map[string]any)
			if content == nil {
				content = map[string]any{}
				entry["content"] = content
			}
			for _, mediaType := range mediaTypes {
				content[mediaType] = map[string]any{"schema": openAPIOutcomeSchemaFrom(operation, when, refOrString(body["from"]), resources), "x-scenery-codec": stringValue(body["codec"])}
			}
		}
	}
	return responses
}

func openAPIOutcomeSchemaFrom(operation Resource, outcome, from string, resources []Resource) map[string]any {
	schema := openAPIOutcomeSchema(operation, outcome, resources)
	parts := strings.Split(from, ".")
	if len(parts) <= 2 {
		return schema
	}
	variantType := operationVariantType(operation, parts[0], parts[1])
	fieldType := recordFieldType(resourcesByAddress(&Manifest{Resources: resources}), operation.Module, variantType, parts[2:])
	if fieldType == nil {
		return schema
	}
	return openAPISchemaForType(fieldType, operation.Module, resources)
}

func openAPIOutcomeSchema(operation Resource, outcome string, resources []Resource) map[string]any {
	parts := strings.Split(outcome, ".")
	if len(parts) == 2 && (parts[0] == "result" || parts[0] == "error") {
		for _, variant := range namedChildren(operation.Spec, parts[0]) {
			if stringValue(variant["name"]) == parts[1] {
				return openAPISchemaForType(variant["type"], operation.Module, resources)
			}
		}
	}
	if strings.HasPrefix(outcome, "dispatch.") && outcome == "dispatch.enqueued" {
		return map[string]any{"type": "object", "additionalProperties": false, "x-scenery-type": "execution_receipt"}
	}
	return map[string]any{"$ref": "#/components/schemas/SceneryProblem"}
}

func openAPIComponents(reachable, all []Resource) map[string]any {
	components := map[string]any{
		"SceneryProblem": map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "additionalProperties": false},
	}
	for _, resource := range reachable {
		name := openAPIComponentName(resource.Module, resource.Name)
		switch resource.Kind {
		case "scenery.record/v1":
			properties := map[string]any{}
			var required []string
			for _, field := range namedChildren(resource.Spec, "field") {
				fieldName := stringValue(field["name"])
				properties[wireName(field, fieldName)] = openAPISchemaForType(field["type"], resource.Module, all)
				if _, optional := optionalInner(field["type"]); !optional {
					required = append(required, wireName(field, fieldName))
				}
			}
			components[name] = map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": resource.Spec["unknown_fields"] == "preserve"}
		case "scenery.enum/v1":
			var values []string
			for _, value := range namedChildren(resource.Spec, "value") {
				label := stringValue(value["name"])
				values = append(values, wireName(value, label))
			}
			components[name] = map[string]any{"type": "string", "enum": values, "x-scenery-open": resource.Spec["open"] == true}
		case "scenery.union/v1":
			var variants []any
			mapping := map[string]any{}
			for _, variant := range namedChildren(resource.Spec, "variant") {
				label := stringValue(variant["name"])
				tag := wireName(variant, label)
				payload := openAPISchemaForType(variant["type"], resource.Module, all)
				variants = append(variants, map[string]any{"allOf": []any{payload, map[string]any{"type": "object", "required": []string{stringValue(resource.Spec["discriminator"])}, "properties": map[string]any{stringValue(resource.Spec["discriminator"]): map[string]any{"const": tag}}}}})
				mapping[tag] = payload["$ref"]
			}
			components[name] = map[string]any{"oneOf": variants, "discriminator": map[string]any{"propertyName": stringValue(resource.Spec["discriminator"]), "mapping": mapping}, "x-scenery-open": resource.Spec["open"] == true}
		}
	}
	return components
}

func openAPISchemaForMapping(mapping map[string]any, operation Resource, resources []Resource) map[string]any {
	target := refOrString(mapping["to"])
	shape := resolveOperationInputShape(resourcesByAddress(&Manifest{Resources: resources}), operation)
	field, whole, ok := resolveOperationInputTarget(operation, shape, target)
	if ok && !whole {
		return openAPISchemaForType(field.Type, operation.Module, resources)
	}
	return openAPISchemaForType(operation.Spec["input"], operation.Module, resources)
}

func openAPISchemaForType(value any, module string, resources []Resource) map[string]any {
	expression := typeExpression(value)
	name, arguments, composite := parseTSExpression(expression)
	if composite {
		switch name {
		case "optional":
			return openAPISchemaForType(map[string]any{"$expression": arguments[0]}, module, resources)
		case "nullable":
			return map[string]any{"anyOf": []any{openAPISchemaForType(map[string]any{"$expression": arguments[0]}, module, resources), map[string]any{"type": "null"}}}
		case "list", "set":
			return map[string]any{"type": "array", "items": openAPISchemaForType(map[string]any{"$expression": arguments[0]}, module, resources), "uniqueItems": name == "set"}
		case "map":
			return map[string]any{"type": "object", "additionalProperties": openAPISchemaForType(map[string]any{"$expression": arguments[0]}, module, resources)}
		case "tuple":
			prefix := make([]any, len(arguments))
			for index, argument := range arguments {
				prefix[index] = openAPISchemaForType(map[string]any{"$expression": argument}, module, resources)
			}
			return map[string]any{"type": "array", "prefixItems": prefix, "minItems": len(prefix), "maxItems": len(prefix)}
		}
	}
	if parts := strings.Split(expression, "."); len(parts) == 2 && (parts[0] == "record" || parts[0] == "enum" || parts[0] == "union") {
		return map[string]any{"$ref": "#/components/schemas/" + openAPIComponentName(module, parts[1])}
	}
	switch expression {
	case "bool":
		return map[string]any{"type": "boolean"}
	case "int32":
		return map[string]any{"type": "integer", "format": "int32"}
	case "uint32":
		return map[string]any{"type": "integer", "minimum": 0, "maximum": "4294967295"}
	case "float32", "float64":
		return map[string]any{"type": "number", "format": expression}
	case "bytes":
		return map[string]any{"type": "string", "contentEncoding": "base64"}
	case "json":
		return map[string]any{}
	case "string":
		return map[string]any{"type": "string"}
	case "uuid", "date", "datetime", "duration", "url", "relative_path":
		format := map[string]string{"datetime": "date-time", "relative_path": "scenery-relative-path"}[expression]
		if format == "" {
			format = expression
		}
		return map[string]any{"type": "string", "format": format}
	case "int", "int64", "uint64", "decimal", "size":
		return map[string]any{"type": "string", "format": "scenery-" + expression}
	case "std.type.problem":
		return map[string]any{"$ref": "#/components/schemas/SceneryProblem"}
	case "std.type.unit":
		return map[string]any{"type": "object", "additionalProperties": false, "maxProperties": 0}
	default:
		return map[string]any{"x-scenery-type": expression}
	}
}

func openAPIComponentName(module, name string) string {
	return goName(module) + goName(name)
}
