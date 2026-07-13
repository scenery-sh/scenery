package compiler

import (
	"sort"
	"strings"
)

// Type descriptors are compiler-owned semantic shapes used to prove whether
// response mappings overlap. Generators may render the same contract, but do
// not own validation semantics.
func tsDescriptor(value any, module string) any {
	if reference := refString(value); reference != "" {
		return tsReferenceDescriptor(reference, module)
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return tsExpressionDescriptor(strings.TrimSpace(raw), module)
		}
	}
	return map[string]any{"kind": "primitive", "name": "json"}
}

func tsReferenceDescriptor(reference, module string) any {
	switch reference {
	case "std.type.problem":
		return map[string]any{"kind": "primitive", "name": "problem"}
	case "std.type.execution_receipt":
		return map[string]any{"kind": "primitive", "name": "execution_receipt"}
	case "std.type.unit":
		return map[string]any{"kind": "primitive", "name": "unit"}
	}
	if primitiveTypes[reference] {
		return map[string]any{"kind": "primitive", "name": reference}
	}
	for _, segment := range []string{"/record/", "/enum/", "/union/"} {
		if strings.Contains(reference, segment) {
			return map[string]any{"kind": "named", "name": reference}
		}
	}
	parts := strings.Split(reference, ".")
	if len(parts) == 2 && (parts[0] == "record" || parts[0] == "enum" || parts[0] == "union") {
		return map[string]any{"kind": "named", "name": module + "/" + parts[0] + "/" + parts[1]}
	}
	return map[string]any{"kind": "primitive", "name": "json"}
}

func tsExpressionDescriptor(raw, module string) any {
	name, arguments, ok := parseTSExpression(raw)
	if !ok {
		return tsReferenceDescriptor(raw, module)
	}
	switch name {
	case "optional", "nullable", "list", "set", "map":
		if len(arguments) != 1 {
			return map[string]any{"kind": "primitive", "name": "json"}
		}
		return map[string]any{"kind": name, "value": tsExpressionDescriptor(arguments[0], module)}
	case "tuple":
		values := make([]any, 0, len(arguments))
		for _, argument := range arguments {
			values = append(values, tsExpressionDescriptor(argument, module))
		}
		return map[string]any{"kind": "tuple", "values": values}
	default:
		return tsReferenceDescriptor(raw, module)
	}
}

func parseTSExpression(raw string) (string, []string, bool) {
	raw = strings.TrimSpace(raw)
	open := strings.IndexByte(raw, '(')
	if open <= 0 || !strings.HasSuffix(raw, ")") {
		return "", nil, false
	}
	name := strings.TrimSpace(raw[:open])
	body := raw[open+1 : len(raw)-1]
	depth, start := 0, 0
	var arguments []string
	for index, char := range body {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", nil, false
			}
		case ',':
			if depth == 0 {
				arguments = append(arguments, strings.TrimSpace(body[start:index]))
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return "", nil, false
	}
	arguments = append(arguments, strings.TrimSpace(body[start:]))
	if len(arguments) == 1 && arguments[0] == "" {
		arguments = nil
	}
	return name, arguments, true
}

func tsNamedDescriptor(resource Resource) any {
	switch resource.Kind {
	case "scenery.record":
		fields := namedChildren(resource.Spec, "field")
		sort.Slice(fields, func(i, j int) bool { return stringValue(fields[i]["name"]) < stringValue(fields[j]["name"]) })
		descriptors := make([]any, 0, len(fields))
		for _, field := range fields {
			name := stringValue(field["name"])
			descriptor := map[string]any{
				"wire": wireName(field, name), "value": tsDescriptor(field["type"], resource.Module),
				"optional": isOptionalType(field["type"]),
			}
			constraints := map[string]any{}
			for _, key := range []string{"minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "immutable", "deprecated"} {
				if value, exists := field[key]; exists {
					constraints[key] = value
				}
			}
			if len(constraints) > 0 {
				descriptor["constraints"] = constraints
			}
			descriptors = append(descriptors, descriptor)
		}
		return map[string]any{"kind": "record", "fields": descriptors, "preserveUnknown": resource.Spec["unknown_fields"] == "preserve"}
	case "scenery.enum":
		var values []string
		for _, value := range namedChildren(resource.Spec, "value") {
			name := stringValue(value["name"])
			values = append(values, wireName(value, name))
		}
		sort.Strings(values)
		return map[string]any{"kind": "enum", "values": values, "open": resource.Spec["open"] == true}
	case "scenery.union":
		variants := map[string]any{}
		for _, variant := range namedChildren(resource.Spec, "variant") {
			name := stringValue(variant["name"])
			variants[wireName(variant, name)] = tsDescriptor(variant["type"], resource.Module)
		}
		discriminator := stringValue(resource.Spec["discriminator"])
		if discriminator == "" {
			discriminator = "kind"
		}
		return map[string]any{"kind": "union", "discriminator": discriminator, "variants": variants, "open": resource.Spec["open"] == true}
	default:
		return map[string]any{"kind": "primitive", "name": "json"}
	}
}
