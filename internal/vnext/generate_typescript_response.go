package vnext

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func renderTSResponseCases(b *strings.Builder, operation Resource, httpSpec map[string]any, resources []Resource) {
	groups := map[string][]map[string]any{}
	for _, response := range namedChildren(httpSpec, "response") {
		status := integerText(response["status"])
		groups[status] = append(groups[status], response)
	}
	statuses := make([]string, 0, len(groups))
	for status := range groups {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		responses := groups[status]
		fmt.Fprintf(b, "    if (response.status === %s) {\n", status)
		maximum := int64(16 << 20)
		if limits, _ := httpSpec["response_limit"].(map[string]any); limits != nil {
			if value, ok := integerValue(limits["bytes"]); ok && value > 0 {
				maximum = int64(value)
			}
		}
		var failures, completions []map[string]any
		for _, response := range responses {
			when := refString(response["when"])
			if strings.HasPrefix(when, "result.") || strings.HasPrefix(when, "error.") || when == "dispatch.enqueued" {
				completions = append(completions, response)
			} else {
				failures = append(failures, response)
			}
		}
		for index, response := range failures {
			when := refString(response["when"])
			fmt.Fprintf(b, "      try {\n        const candidateResponse%d = response.clone();\n", index)
			renderTSResponsePayload(b, operation, response, resources, fmt.Sprintf("candidateResponse%d", index), maximum, "        ")
			if strings.HasPrefix(when, "system.") {
				fmt.Fprintf(b, "        if (Runtime.isProblemCode(payload, %q)) throw new Runtime.SceneryClientError(\"server\", binding, %q);\n", when, "server returned "+when)
			} else {
				name := defaultString(stringValue(response["name"]), lastRef(when))
				fmt.Fprintf(b, "        if (Runtime.isProblemCode(payload, %q)) return { kind: \"failure\", name: %q, problem: payload as Types.Problem } as Types.%sOutcome;\n", when, name, goName(operation.Name))
			}
			b.WriteString("      } catch (cause) {\n        if (!(cause instanceof Runtime.SceneryClientError) || cause.code !== \"contract_violation\") throw cause;\n      }\n")
		}
		if len(completions) > 0 {
			fmt.Fprintf(b, "      const completionMatches: Types.%sOutcome[] = [];\n", goName(operation.Name))
		}
		for index, completion := range completions {
			fmt.Fprintf(b, "      try {\n        const completionResponse%d = response.clone();\n", index)
			renderTSResponsePayload(b, operation, completion, resources, fmt.Sprintf("completionResponse%d", index), maximum, "        ")
			when := refString(completion["when"])
			variant := lastRef(when)
			kind, field, valueType := "result", "value", operationVariantType(operation, "result", variant)
			if strings.HasPrefix(when, "error.") {
				kind, field, valueType = "error", "problem", operationVariantType(operation, "error", variant)
			} else if when == "dispatch.enqueued" {
				kind, field, valueType = "enqueue", "receipt", map[string]any{"$ref": "std.type.execution_receipt"}
			}
			name := stringValue(completion["name"])
			if name == "" {
				name = variant
			}
			fmt.Fprintf(b, "        completionMatches.push({ kind: %q, name: %q, %s: payload as %s } as Types.%sOutcome);\n", kind, name, field, tsClientType(valueType), goName(operation.Name))
			b.WriteString("      } catch (cause) {\n        if (!(cause instanceof Runtime.SceneryClientError) || cause.code !== \"contract_violation\") throw cause;\n      }\n")
		}
		if len(completions) > 0 {
			b.WriteString("      if (completionMatches.length === 1) return completionMatches[0]!;\n")
		}
		b.WriteString("      throw new Runtime.SceneryClientError(\"contract_violation\", binding, \"response body contradicts the contract\");\n    }\n")
	}
}

func renderTSResponsePayload(b *strings.Builder, operation Resource, response map[string]any, resources []Resource, responseVariable string, maximum int64, indent string) {
	when := refString(response["when"])
	body, _ := response["body"].(map[string]any)
	fmt.Fprintf(b, "%slet payload: unknown = undefined;\n", indent)
	if body == nil {
		fmt.Fprintf(b, "%sawait Runtime.assertEmptyResponse(%s, binding, %d);\n", indent, responseVariable, maximum)
	} else {
		valueType, path := tsResponseMappedValue(operation, when, refOrString(body["from"]), resources)
		codec := stringValue(body["codec"])
		produced := literalStringListFromValue(body["produced_media_types"])
		if len(produced) == 0 {
			produced = []string{defaultHTTPMediaType(codec)}
		}
		encodedProduced, _ := json.Marshal(produced)
		fmt.Fprintf(b, "%sconst decoded = await Runtime.decodeResponseBody(%s, %q, %s, %s, typeRegistry, binding, %d);\n", indent, responseVariable, codec, encodedProduced, tsDescriptorLiteral(valueType, operation.Module), maximum)
		fmt.Fprintf(b, "%spayload = Runtime.mergeResponseValue(payload, %s, decoded, binding);\n", indent, tsResponsePathLiteral(path))
	}
	for _, header := range namedChildren(response, "header") {
		valueType, path := tsResponseMappedValue(operation, when, refOrString(header["from"]), resources)
		encoding := defaultString(stringValue(header["encoding"]), "repeated")
		fmt.Fprintf(b, "%spayload = Runtime.mergeResponseValue(payload, %s, Runtime.decodeResponseHeader(response, %q, %q, %s, typeRegistry, binding), binding);\n", indent, tsResponsePathLiteral(path), stringValue(header["name"]), encoding, tsDescriptorLiteral(valueType, operation.Module))
	}
	for _, cookie := range namedChildren(response, "cookie") {
		valueType, path := tsResponseMappedValue(operation, when, refOrString(cookie["from"]), resources)
		fmt.Fprintf(b, "%spayload = Runtime.mergeResponseValue(payload, %s, Runtime.decodeResponseCookie(response, %q, %s, typeRegistry, binding), binding);\n", indent, tsResponsePathLiteral(path), stringValue(cookie["name"]), tsDescriptorLiteral(valueType, operation.Module))
	}
	fmt.Fprintf(b, "%sif (payload === undefined) payload = {};\n", indent)
}

func tsResponsePathLiteral(path []string) string {
	if len(path) == 0 {
		return "[]"
	}
	encoded, _ := json.Marshal(path)
	return string(encoded)
}

func tsResponseMappedValue(operation Resource, when, from string, resources []Resource) (any, []string) {
	valueType := tsResponseValueType(operation, when)
	parts := strings.Split(from, ".")
	if len(parts) <= 2 {
		return valueType, nil
	}
	semanticPath := parts[2:]
	if refOrString(valueType) == "std.type.problem" {
		return map[string]any{"$ref": "string"}, semanticPath
	}
	if refOrString(valueType) == "std.type.execution_receipt" {
		property := map[string]string{
			"durable_identity":  "durableIdentity",
			"execution_id":      "executionId",
			"accepted_revision": "acceptedRevision",
			"status_url":        "statusUrl",
		}[semanticPath[0]]
		fieldType := any(map[string]any{"$ref": "string"})
		if semanticPath[0] == "status_url" {
			fieldType = map[string]any{"$ref": "url"}
		}
		return fieldType, []string{property}
	}
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	fieldType := recordFieldType(resourceMap, operation.Module, valueType, semanticPath)
	if fieldType == nil {
		return valueType, nil
	}
	return fieldType, tsRecordPropertyPath(resourceMap, operation.Module, valueType, semanticPath)
}

func tsRecordPropertyPath(resources map[string]Resource, module string, value any, path []string) []string {
	current := value
	properties := make([]string, 0, len(path))
	for _, name := range path {
		record, ok := recordResourceForType(resources, module, current)
		if !ok {
			return nil
		}
		module = record.Module
		found := false
		for _, field := range namedChildren(record.Spec, "field") {
			if stringValue(field["name"]) == name {
				properties = append(properties, tsName(name))
				current = field["type"]
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return properties
}

func tsResponseValueType(operation Resource, when string) any {
	variant := lastRef(when)
	if strings.HasPrefix(when, "result.") {
		return operationVariantType(operation, "result", variant)
	}
	if strings.HasPrefix(when, "error.") {
		return operationVariantType(operation, "error", variant)
	}
	if when == "dispatch.enqueued" {
		return map[string]any{"$ref": "std.type.execution_receipt"}
	}
	return map[string]any{"$ref": "std.type.problem"}
}
