package compiler

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var validationCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

var primitiveTypes = map[string]bool{
	"bool": true, "int": true, "int32": true, "int64": true,
	"uint32": true, "uint64": true, "decimal": true, "float32": true,
	"float64": true, "string": true, "bytes": true, "uuid": true,
	"date": true, "datetime": true, "duration": true, "size": true,
	"url": true, "relative_path": true, "json": true,
}

func validateTypeSystem(resources []Resource) []Diagnostic {
	types := map[string]bool{}
	byAddress := map[string]Resource{}
	for _, resource := range resources {
		byAddress[resource.Address] = resource
		switch resource.Kind {
		case "scenery.record", "scenery.enum", "scenery.union":
			types[resource.Module+"/"+strings.TrimPrefix(resource.Kind, "scenery.")+"/"+resource.Name] = true
		}
	}
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.record":
			diagnostics = append(diagnostics, validateRecord(resource, types)...)
		case "scenery.enum":
			diagnostics = append(diagnostics, validateNamedWireChildren(resource, "value")...)
		case "scenery.union":
			diagnostics = append(diagnostics, validateNamedWireChildren(resource, "variant")...)
			for _, variant := range namedChildren(resource.Spec, "variant") {
				diagnostics = append(diagnostics, validateTypeValue(resource, variant["type"], types)...)
			}
			diagnostics = append(diagnostics, validateUnion(resource, byAddress)...)
		case "scenery.operation":
			diagnostics = append(diagnostics, validateTypeValue(resource, resource.Spec["input"], types)...)
			for _, kind := range []string{"result", "error"} {
				for _, child := range namedChildren(resource.Spec, kind) {
					diagnostics = append(diagnostics, validateTypeValue(resource, child["type"], types)...)
				}
			}
		case "scenery.event":
			diagnostics = append(diagnostics, validateTypeValue(resource, resource.Spec["payload"], types)...)
		case "scenery.entity":
			diagnostics = append(diagnostics, validateTypeValue(resource, resource.Spec["type"], types)...)
		case "scenery.view":
			diagnostics = append(diagnostics, validateTypeValue(resource, resource.Spec["input"], types)...)
			diagnostics = append(diagnostics, validateTypeValue(resource, resource.Spec["result"], types)...)
		}
	}
	return diagnostics
}

func validateUnion(resource Resource, resources map[string]Resource) []Diagnostic {
	discriminator, _ := resource.Spec["discriminator"].(string)
	var diagnostics []Diagnostic
	if strings.TrimSpace(discriminator) == "" {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN1208", Severity: "error", Message: "tagged union requires a non-empty discriminator", Address: resource.Address})
		return diagnostics
	}
	if resource.Spec["open"] == true && resource.Spec["unknown_variant"] == nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN1209", Severity: "error", Message: "open union requires an unknown_variant preservation declaration", Address: resource.Address})
	}
	for _, variant := range namedChildren(resource.Spec, "variant") {
		reference := refString(variant["type"])
		parts := strings.Split(reference, ".")
		if len(parts) != 2 || parts[0] != "record" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1210", Severity: "error", Message: "union variants must use record payloads", Address: resource.Address})
			continue
		}
		record, ok := resources[resourceAddress(resource.Module, "record", parts[1])]
		if !ok {
			continue
		}
		for _, field := range namedChildren(record.Spec, "field") {
			name := stringValue(field["name"])
			if wireName(field, name) == discriminator {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1211", Severity: "error", Message: "union discriminator collides with payload field " + name, Address: resource.Address})
			}
		}
	}
	return diagnostics
}

func validateReferences(resources []Resource) []Diagnostic {
	known := map[string]bool{}
	moduleInputs := map[string]map[string]bool{}
	for _, resource := range resources {
		known[resource.Address] = true
		if resource.Kind == "scenery.module" {
			inputs, _ := resource.Spec["inputs"].(map[string]any)
			instance := moduleInstancePath(resource)
			moduleInputs[instance] = map[string]bool{}
			for name := range inputs {
				moduleInputs[instance][name] = true
			}
		}
	}
	reserved := map[string]bool{"std": true, "module": true, "principal": true, "context": true, "result": true, "error": true, "transport": true, "admission": true, "dispatch": true, "system": true}
	var diagnostics []Diagnostic
	for _, resource := range resources {
		walkRefs(resource.Spec, "/spec", func(path, reference string) {
			referenceModule := resource.Module
			if resource.Kind == "scenery.module" && (strings.HasPrefix(path, "/spec/exports") || strings.HasPrefix(path, "/spec/export_metadata")) {
				referenceModule = moduleInstancePath(resource)
			}
			if primitiveTypes[reference] || strings.HasPrefix(reference, "std.") {
				return
			}
			parts := strings.Split(reference, ".")
			if len(parts) > 0 && parts[0] == "var" {
				if len(parts) != 2 || !moduleInputs[resource.Module][parts[1]] {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN1207", Severity: "error", Message: "unknown module input reference " + reference, Address: resource.Address, Path: path})
				}
				return
			}
			if len(parts) >= 3 && parts[0] == "operation" {
				if !known[resourceAddress(referenceModule, "operation", parts[1])] {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN1207", Severity: "error", Message: "unknown operation reference " + reference, Address: resource.Address, Path: path})
				}
				return
			}
			if len(parts) > 0 && reserved[parts[0]] {
				return
			}
			if known[reference] {
				return
			}
			if len(parts) == 2 {
				local := resourceAddress(referenceModule, parts[0], parts[1])
				global := resourceAddress("app", parts[0], parts[1])
				if known[local] || known[global] {
					return
				}
			}
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1207", Severity: "error", Message: "unknown resource reference " + reference, Address: resource.Address, Path: path})
		})
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}

func validateRecord(resource Resource, types map[string]bool) []Diagnostic {
	var diagnostics []Diagnostic
	wires := map[string]string{}
	fieldTypes := map[string]string{}
	for _, field := range namedChildren(resource.Spec, "field") {
		name, _ := field["name"].(string)
		fieldTypes[name] = typeExpression(field["type"])
		wire := wireName(field, name)
		if previous, exists := wires[wire]; exists {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1204", Severity: "error", Message: fmt.Sprintf("record fields %s and %s use duplicate wire name %q", previous, name, wire), Address: resource.Address})
		} else {
			wires[wire] = name
		}
		diagnostics = append(diagnostics, validateTypeValue(resource, field["type"], types)...)
	}
	seenValidations := map[string]bool{}
	for _, validation := range namedChildren(resource.Spec, "validation") {
		name := stringValue(validation["name"])
		code, message := stringValue(validation["code"]), stringValue(validation["message"])
		expression, path := expressionText(validation["when"]), refString(validation["path"])
		if name == "" || seenValidations[name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1230", Severity: "error", Message: "record validations require unique non-empty names", Address: resource.Address})
		}
		seenValidations[name] = true
		if expression == "" || !validationCodePattern.MatchString(code) || message == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1231", Severity: "error", Message: "record validation requires when, upper-case code, and message", Address: resource.Address})
		}
		prefix := "record." + resource.Name + "."
		fieldName, ok := strings.CutPrefix(path, prefix)
		if !ok || strings.Contains(fieldName, ".") || fieldTypes[fieldName] == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1232", Severity: "error", Message: "record validation path must reference a field on the validated record", Address: resource.Address})
		}
		if expression != "" {
			if err := validateRecordValidationExpression(expression, fieldTypes); err != nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1233", Severity: "error", Message: err.Error(), Address: resource.Address})
			}
		}
	}
	return diagnostics
}

func validateNamedWireChildren(resource Resource, childKind string) []Diagnostic {
	seenNames, seenWires := map[string]bool{}, map[string]string{}
	var diagnostics []Diagnostic
	for _, child := range namedChildren(resource.Spec, childKind) {
		name, _ := child["name"].(string)
		if seenNames[name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1205", Severity: "error", Message: "duplicate " + childKind + " " + name, Address: resource.Address})
		}
		seenNames[name] = true
		wire := wireName(child, name)
		if previous, exists := seenWires[wire]; exists {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1206", Severity: "error", Message: fmt.Sprintf("%s %s and %s use duplicate wire value %q", childKind, previous, name, wire), Address: resource.Address})
		}
		seenWires[wire] = name
	}
	return diagnostics
}

func validateTypeValue(resource Resource, value any, types map[string]bool) []Diagnostic {
	if value == nil {
		return []Diagnostic{{Code: "SCN1201", Severity: "error", Message: "missing type", Address: resource.Address}}
	}
	if _, literalString := value.(string); literalString {
		return []Diagnostic{{Code: "SCN1202", Severity: "error", Message: "type references are expressions, not strings", Address: resource.Address}}
	}
	var names []string
	if ref := refString(value); ref != "" {
		names = []string{ref}
	} else if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			names = typeExpressionNames(raw)
		}
	}
	if len(names) == 0 {
		return []Diagnostic{{Code: "SCN1201", Severity: "error", Message: "invalid type expression", Address: resource.Address}}
	}
	var diagnostics []Diagnostic
	for _, name := range names {
		if primitiveTypes[name] || name == "std.type.problem" || name == "std.type.unit" {
			continue
		}
		if strings.Contains(name, "/") && types[name] {
			continue
		}
		parts := strings.Split(name, ".")
		if len(parts) != 2 || (parts[0] != "record" && parts[0] != "enum" && parts[0] != "union") || !types[resource.Module+"/"+parts[0]+"/"+parts[1]] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1203", Severity: "error", Message: "unknown type reference " + name, Address: resource.Address})
		}
	}
	return diagnostics
}

func moduleInstancePath(resource Resource) string {
	if resource.Module == "app" || resource.Module == "" {
		return resource.Name
	}
	return resource.Module + "/" + resource.Name
}

func typeExpressionNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	for _, wrapper := range []string{"optional", "nullable", "list", "set", "map"} {
		prefix := wrapper + "("
		if strings.HasPrefix(raw, prefix) && strings.HasSuffix(raw, ")") {
			return typeExpressionNames(strings.TrimSpace(raw[len(prefix) : len(raw)-1]))
		}
	}
	if strings.HasPrefix(raw, "tuple(") && strings.HasSuffix(raw, ")") {
		var names []string
		for _, item := range splitTypeArguments(raw[len("tuple(") : len(raw)-1]) {
			names = append(names, typeExpressionNames(item)...)
		}
		return names
	}
	return []string{raw}
}

func splitTypeArguments(value string) []string {
	depth, start := 0, 0
	var parts []string
	for index, char := range value {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(value[start:index]))
				start = index + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(value[start:]))
	return parts
}
