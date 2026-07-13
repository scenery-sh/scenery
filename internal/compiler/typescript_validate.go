package compiler

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func validateTypeScriptResources(resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, target := range resources {
		if target.Kind != "scenery.typescript-client" {
			continue
		}
		diagnostics = append(diagnostics, validateTypeScriptTarget(target, resources)...)
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}

func validateTypeScriptTarget(target Resource, resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	diagnostic := func(code, message string) {
		diagnostics = append(diagnostics, Diagnostic{Code: code, Severity: "error", Message: message, Address: target.Address})
	}
	if target.Spec["module"] != "esm" {
		diagnostic("SCN6301", "TypeScript client module must be esm")
	}
	if target.Spec["runtime"] != "fetch" {
		diagnostic("SCN6302", "TypeScript client runtime must be fetch")
	}
	packageName, _ := target.Spec["package"].(string)
	if packageName == "" || strings.ContainsAny(packageName, " \\") {
		diagnostic("SCN6303", "TypeScript client package name is invalid")
	}
	outputRoot, _ := target.Spec["output_root"].(string)
	cleanOutput := filepath.Clean(filepath.FromSlash(outputRoot))
	if outputRoot == "" || filepath.IsAbs(outputRoot) || cleanOutput == ".." || strings.HasPrefix(cleanOutput, ".."+string(filepath.Separator)) {
		diagnostic("SCN6304", "TypeScript client output_root must be workspace-relative")
	}
	if len(anyList(target.Spec["gateways"])) == 0 {
		diagnostic("SCN6305", "TypeScript client requires at least one gateway")
	}
	bindings := publicHTTPBindings(resources, target)
	diagnostics = append(diagnostics, validateTypeScriptNames(target, resources, bindings)...)
	operations := resourcesByKind(resources, "scenery.operation")
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	for _, binding := range bindings {
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		operation := operations[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
		for _, header := range namedChildren(httpSpec, "header") {
			if defaultString(stringValue(header["encoding"]), "repeated") != "repeated" {
				continue
			}
			valueType := tsOperationFieldType(operation, resources, header["to"])
			expression := unwrapHTTPType(typeExpression(valueType))
			if strings.HasPrefix(expression, "list(") || strings.HasPrefix(expression, "set(") {
				diagnostic("SCN6316", "fetch TypeScript client cannot preserve repeated request header field lines for "+binding.Address)
			}
		}
	}
	if retry, _ := target.Spec["retry"].(map[string]any); retry != nil {
		if retry["policy"] != "scenery.retry.idempotent" {
			diagnostic("SCN6307", "TypeScript retry policy must be scenery.retry.idempotent")
		}
		attempts, ok := integerValue(retry["maximum_attempts"])
		if !ok || attempts < 2 || attempts > 10 {
			diagnostic("SCN6308", "TypeScript retry maximum_attempts must be between 2 and 10")
		}
		if delay, present := retry["maximum_delay_milliseconds"]; present {
			value, valid := integerValue(delay)
			if !valid || value < 0 || value > 86_400_000 {
				diagnostic("SCN6313", "TypeScript retry maximum_delay_milliseconds is invalid")
			}
		}
		seenStatuses := map[int]bool{}
		for _, statusValue := range anyList(retry["statuses"]) {
			status, valid := integerValue(statusValue)
			if !valid || status < 400 || status > 599 || seenStatuses[status] {
				diagnostic("SCN6314", "TypeScript retry statuses must be unique HTTP error statuses")
				continue
			}
			seenStatuses[status] = true
		}
		for _, binding := range bindings {
			operation := operations[binding.Module+"/operation/"+lastRef(refString(binding.Spec["operation"]))]
			idempotency, _ := operation.Spec["idempotency"].(map[string]any)
			if idempotency == nil || !validKeyedIdempotency(operation, resourceMap) {
				diagnostic("SCN6309", "retry-selected binding "+binding.Address+" does not reference an idempotent operation")
			}
			httpSpec, _ := binding.Spec["http"].(map[string]any)
			body, _ := httpSpec["body"].(map[string]any)
			if body != nil && body["codec"] == "multipart" {
				diagnostic("SCN6315", "retry-selected binding "+binding.Address+" uses a non-replayable multipart body")
			}
		}
	}
	return diagnostics
}

func validateTypeScriptNames(target Resource, resources, bindings []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	seenTypes := map[string]string{
		"JsonValue":          "generated JSON value type",
		"JsonNumber":         "generated exact JSON number type",
		"UUIDString":         "generated UUID scalar type",
		"DateString":         "generated date scalar type",
		"DateTimeString":     "generated datetime scalar type",
		"DurationString":     "generated duration scalar type",
		"DecimalString":      "generated decimal scalar type",
		"URLString":          "generated URL scalar type",
		"RelativePathString": "generated relative-path scalar type",
		"Problem":            "generated problem type",
		"EnqueueReceipt":     "generated enqueue receipt type",
	}
	addType := func(name, owner string) {
		if previous, exists := seenTypes[name]; exists && previous != owner {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6310", Severity: "error", Message: fmt.Sprintf("generated TypeScript type name %s collides between %s and %s", name, previous, owner), Address: target.Address})
			return
		}
		seenTypes[name] = owner
	}
	for _, resource := range reachableResources(resources, bindings) {
		switch resource.Kind {
		case "scenery.record", "scenery.enum", "scenery.union":
			addType(goName(resource.Name), resource.Address)
		case "scenery.operation":
			name := goName(resource.Name)
			if tsType(resource.Spec["input"]) != name+"Input" {
				addType(name+"Input", resource.Address+" input")
			}
			addType(name+"Outcome", resource.Address+" outcome")
		}
		if resource.Kind == "scenery.record" {
			seenFields := map[string]string{}
			if resource.Spec["unknown_fields"] == "preserve" {
				seenFields["unknownFields"] = "generated unknown-field storage"
			}
			for _, field := range namedChildren(resource.Spec, "field") {
				semantic := stringValue(field["name"])
				name := tsName(semantic)
				if previous, exists := seenFields[name]; exists && previous != semantic {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN6311", Severity: "error", Message: fmt.Sprintf("generated TypeScript field name %s collides in %s", name, resource.Address), Address: target.Address})
				}
				seenFields[name] = semantic
			}
		}
	}
	counts, methods := map[string]int{}, map[string]string{}
	for _, binding := range bindings {
		counts[lastRef(refString(binding.Spec["operation"]))]++
	}
	for _, binding := range bindings {
		operation := lastRef(refString(binding.Spec["operation"]))
		method := tsName(operation)
		if counts[operation] > 1 {
			method += "Via" + goName(binding.Name)
		}
		if previous, exists := methods[method]; exists {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6312", Severity: "error", Message: fmt.Sprintf("generated TypeScript method %s collides between %s and %s", method, previous, binding.Address), Address: target.Address})
		}
		if method == "constructor" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6312", Severity: "error", Message: "generated TypeScript method constructor collides with the client constructor", Address: target.Address})
		}
		methods[method] = binding.Address
	}
	return diagnostics
}

func resourcesByKind(resources []Resource, kind string) map[string]Resource {
	result := map[string]Resource{}
	for _, resource := range resources {
		if resource.Kind == kind {
			result[resource.Address] = resource
		}
	}
	return result
}

func integerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	case string:
		parsed, err := strconv.Atoi(typed)
		return parsed, err == nil
	case map[string]any:
		if typed["$scalar"] != "int" {
			return 0, false
		}
		parsed, err := strconv.Atoi(stringValue(typed["value"]))
		return parsed, err == nil
	default:
		return 0, false
	}
}
