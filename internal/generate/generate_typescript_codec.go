package generate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func renderTSRegistry(resources []Resource) string {
	entries := map[string]any{}
	for _, resource := range resources {
		key := tsRegistryKey(resource)
		if key == "" {
			continue
		}
		entries[key] = tsNamedDescriptor(resource)
	}
	encoded, _ := json.Marshal(entries)
	return "const typeRegistry: Runtime.TypeRegistry = Object.freeze(" + string(encoded) + " as const);\n"
}

func tsRegistryKey(resource Resource) string {
	switch resource.Kind {
	case "scenery.record":
		return resource.Module + "/record/" + resource.Name
	case "scenery.enum":
		return resource.Module + "/enum/" + resource.Name
	case "scenery.union":
		return resource.Module + "/union/" + resource.Name
	default:
		return ""
	}
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
				"property": tsName(name),
				"wire":     wireName(field, name),
				"value":    tsDescriptor(field["type"], resource.Module),
				"optional": isOptionalType(field["type"]),
			}
			if constraints := tsFieldConstraints(field); len(constraints) > 0 {
				descriptor["constraints"] = constraints
			}
			descriptors = append(descriptors, descriptor)
		}
		record := map[string]any{"kind": "record", "fields": descriptors, "preserveUnknown": resource.Spec["unknown_fields"] == "preserve"}
		if validations := tsValidationDescriptors(resource); len(validations) > 0 {
			record["validations"] = validations
		}
		return record
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

func tsFieldConstraints(field map[string]any) map[string]any {
	constraints := map[string]any{}
	for _, key := range []string{"minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "immutable", "deprecated"} {
		if value, exists := field[key]; exists {
			constraints[key] = value
		}
	}
	return constraints
}

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
	if reference == "std.type.problem" {
		return map[string]any{"kind": "primitive", "name": "problem"}
	}
	if reference == "std.type.execution_receipt" {
		return map[string]any{"kind": "primitive", "name": "execution_receipt"}
	}
	if reference == "std.type.unit" {
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

func tsDescriptorLiteral(value any, module string) string {
	encoded, _ := json.Marshal(tsDescriptor(value, module))
	return string(encoded) + " as const"
}

func tsOperationFieldType(operation Resource, resources []Resource, target any) any {
	targetName := lastRef(refOrString(target))
	inputRef := refString(operation.Spec["input"])
	parts := strings.Split(inputRef, ".")
	if len(parts) != 2 || parts[0] != "record" {
		return operation.Spec["input"]
	}
	for _, resource := range resources {
		if resource.Module != operation.Module || resource.Kind != "scenery.record" || resource.Name != parts[1] {
			continue
		}
		for _, field := range namedChildren(resource.Spec, "field") {
			if stringValue(field["name"]) == targetName {
				return field["type"]
			}
		}
	}
	return operation.Spec["input"]
}

func tsBodyDescriptor(body map[string]any, operation Resource, resources []Resource) (valueExpression, descriptor string) {
	target := refOrString(body["to"])
	if target != "operation."+operation.Name+".input" {
		return "input." + tsInputTargetProperty(body["to"]), tsDescriptorLiteral(tsOperationFieldType(operation, resources, body["to"]), operation.Module)
	}
	selected := tsSelectedBodyFields(body, operation, resources)
	if selected == nil {
		return "input", tsDescriptorLiteral(operation.Spec["input"], operation.Module)
	}
	properties := make([]string, 0, len(selected))
	fields := make([]any, 0, len(selected))
	for _, field := range selected {
		name := stringValue(field["name"])
		property := tsName(name)
		properties = append(properties, fmt.Sprintf("%s: input.%s", property, property))
		descriptor := map[string]any{"property": property, "wire": wireName(field, name), "value": tsDescriptor(field["type"], operation.Module), "optional": isOptionalType(field["type"])}
		if constraints := tsFieldConstraints(field); len(constraints) > 0 {
			descriptor["constraints"] = constraints
		}
		fields = append(fields, descriptor)
	}
	descriptorBytes, _ := json.Marshal(map[string]any{"kind": "record", "fields": fields, "preserveUnknown": false})
	return "{ " + strings.Join(properties, ", ") + " }", string(descriptorBytes) + " as const"
}

func tsMultipartBodyDescriptor(httpSpec map[string]any, operation Resource, resources []Resource) string {
	body, _ := httpSpec["body"].(map[string]any)
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	shape := resolveOperationInputShape(resourceMap, operation)
	requestLimits, _ := httpSpec["request_limit"].(map[string]any)
	defaultFileBytes, ok := integerValue(requestLimits["multipart_file_part_bytes"])
	if !ok {
		defaultFileBytes = 16 << 20
	}
	defaultFieldBytes, ok := integerValue(requestLimits["multipart_non_file_part_bytes"])
	if !ok {
		defaultFieldBytes = 1 << 20
	}
	maximumBytes, ok := integerValue(body["max_compressed_bytes"])
	if !ok {
		maximumBytes, ok = integerValue(requestLimits["multipart_body_bytes"])
	}
	if !ok {
		maximumBytes = 32 << 20
	}
	maximumParts, ok := integerValue(body["max_parts"])
	if !ok {
		maximumParts, ok = integerValue(requestLimits["multipart_parts"])
	}
	if !ok {
		maximumParts = 128
	}
	parts := make([]map[string]any, 0)
	for _, part := range namedChildren(body, "part") {
		field, whole, valid := resolveOperationInputTarget(operation, shape, refOrString(part["to"]))
		if !valid || whole {
			continue
		}
		maximum, ok := integerValue(part["max_bytes"])
		if !ok {
			maximum = defaultFieldBytes
			if stringValue(part["kind"]) == "file" {
				maximum = defaultFileBytes
			}
		}
		mediaTypes := literalStringListFromValue(part["media_types"])
		if mediaTypes == nil {
			mediaTypes = []string{}
		}
		descriptor := map[string]any{
			"name": stringValue(part["name"]), "property": tsName(field.Name), "kind": stringValue(part["kind"]),
			"mediaTypes": mediaTypes, "maxBytes": maximum, "multiple": part["multiple"] == true,
			"optional": field.Optional, "retainFilename": part["retain_filename"] == true,
			"value": tsDescriptor(field.Type, operation.Module),
		}
		if part["retain_filename"] == true {
			descriptor["fileProperties"] = map[string]string{"bytes": tsName("bytes"), "filename": tsName("filename"), "mediaType": tsName("media_type")}
		}
		parts = append(parts, descriptor)
	}
	sort.Slice(parts, func(i, j int) bool { return stringValue(parts[i]["name"]) < stringValue(parts[j]["name"]) })
	encoded, _ := json.Marshal(map[string]any{
		"value": tsDescriptor(operation.Spec["input"], operation.Module),
		"parts": parts, "maxParts": maximumParts, "maxBytes": maximumBytes,
	})
	return string(encoded) + " as const"
}

func tsSelectedBodyFields(body map[string]any, operation Resource, resources []Resource) []map[string]any {
	inputRef := refString(operation.Spec["input"])
	parts := strings.Split(inputRef, ".")
	if len(parts) != 2 || parts[0] != "record" || (body["include"] == nil && body["except"] == nil) {
		return nil
	}
	var fields []map[string]any
	for _, resource := range resources {
		if resource.Module == operation.Module && resource.Kind == "scenery.record" && resource.Name == parts[1] {
			fields = namedChildren(resource.Spec, "field")
			break
		}
	}
	include, exclude := map[string]bool{}, map[string]bool{}
	for _, item := range anyList(body["include"]) {
		include[lastRef(refOrString(item))] = true
	}
	for _, item := range anyList(body["except"]) {
		exclude[lastRef(refOrString(item))] = true
	}
	selected := fields[:0]
	for _, field := range fields {
		name := stringValue(field["name"])
		if (len(include) == 0 || include[name]) && !exclude[name] {
			selected = append(selected, field)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return stringValue(selected[i]["name"]) < stringValue(selected[j]["name"]) })
	return selected
}

func anyList(value any) []any {
	items, _ := value.([]any)
	return items
}
