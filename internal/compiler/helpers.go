package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"scenery.sh/internal/scn"
	"scenery.sh/internal/spec"
)

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	resources := map[string]Resource{}
	if manifest != nil {
		for _, resource := range manifest.Resources {
			resources[resource.Address] = resource
		}
	}
	return resources
}

func usesGoImplementation(resources []Resource) bool {
	for _, resource := range resources {
		if resource.Kind == "scenery.go-target" {
			return true
		}
	}
	return false
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func wireName(field map[string]any, fallback string) string {
	if name := stringValue(field["wire_name"]); name != "" {
		return name
	}
	if name := stringValue(field["name"]); name != "" {
		return name
	}
	return fallback
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func goName(value string) string {
	var builder strings.Builder
	for _, part := range strings.FieldsFunc(value, func(char rune) bool { return char == '_' || char == '-' }) {
		if part != "" {
			builder.WriteString(strings.ToUpper(part[:1]))
			builder.WriteString(part[1:])
		}
	}
	return builder.String()
}

func goPackageName(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(value, "_", ""), "-", ""))
}

func lastRef(value string) string {
	value = strings.TrimSpace(value)
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	parts := strings.Split(value, ".")
	return parts[len(parts)-1]
}

func literalStringListFromValue(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func anyList(value any) []any {
	items, _ := value.([]any)
	return items
}

func defaultHTTPMediaType(codec string) string {
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
	default:
		return "application/json"
	}
}

func typeValueNames(value any) []string {
	if reference := refString(value); reference != "" {
		return []string{reference}
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return typeExpressionNames(raw)
		}
	}
	return nil
}

func validSemanticName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func numericValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	case map[string]any:
		parsed, _ := strconv.ParseFloat(stringValue(typed), 64)
		return parsed
	default:
		return 0
	}
}

func reachableResources(resources, bindings []Resource) []Resource {
	byAddress := map[string]Resource{}
	operations := map[string]Resource{}
	for _, binding := range bindings {
		address := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		operations[address] = Resource{}
	}
	for _, resource := range resources {
		byAddress[resource.Address] = resource
		if _, selected := operations[resource.Address]; selected {
			operations[resource.Address] = resource
		}
	}
	selected := map[string]Resource{}
	var addType func(string, any)
	addType = func(module string, value any) {
		for _, name := range typeValueNames(value) {
			address := name
			if !strings.Contains(name, "/") {
				parts := strings.Split(name, ".")
				if len(parts) != 2 || (parts[0] != "record" && parts[0] != "enum" && parts[0] != "union") {
					continue
				}
				address = resourceAddress(module, parts[0], parts[1])
			}
			if _, exists := selected[address]; exists {
				continue
			}
			resource, exists := byAddress[address]
			if !exists {
				continue
			}
			selected[address] = resource
			switch resource.Kind {
			case "scenery.record":
				for _, field := range namedChildren(resource.Spec, "field") {
					addType(resource.Module, field["type"])
				}
			case "scenery.union":
				for _, variant := range namedChildren(resource.Spec, "variant") {
					addType(resource.Module, variant["type"])
				}
			}
		}
	}
	for address, operation := range operations {
		if operation.Address == "" {
			continue
		}
		selected[address] = operation
		addType(operation.Module, operation.Spec["input"])
		for _, kind := range []string{"result", "error"} {
			for _, variant := range namedChildren(operation.Spec, kind) {
				addType(operation.Module, variant["type"])
			}
		}
	}
	result := make([]Resource, 0, len(selected))
	for _, resource := range selected {
		result = append(result, resource)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func operationVariantType(operation Resource, kind, name string) any {
	for _, variant := range namedChildren(operation.Spec, kind) {
		if variant["name"] == name {
			return variant["type"]
		}
	}
	return nil
}

func publicHTTPBindings(resources []Resource, target Resource) []Resource {
	var result []Resource
	operations := map[string]Resource{}
	exported, exportDeclared := exportedOperations(resources)
	gateways, include := map[string]bool{}, map[string]bool{}
	for _, value := range anyList(target.Spec["gateways"]) {
		gateways[refOrString(value)] = true
		gateways[lastRef(refOrString(value))] = true
	}
	for _, value := range anyList(target.Spec["include"]) {
		include[refOrString(value)] = true
		include[lastRef(refOrString(value))] = true
	}
	for _, resource := range resources {
		if resource.Kind == "scenery.operation" {
			operations[resource.Address] = resource
			operations[resource.Module+"/operation/"+resource.Name] = resource
		}
	}
	for _, binding := range resources {
		if binding.Kind != "scenery.binding" || binding.Origin.Kind != "authored" || stringValue(binding.Spec["protocol"]) != "http" {
			continue
		}
		operationRef := refString(binding.Spec["operation"])
		operation := operations[operationRef]
		if operation.Address == "" {
			operation = operations[binding.Module+"/operation/"+lastRef(operationRef)]
		}
		if exportDeclared[binding.Module] && !exported[operation.Address] {
			continue
		}
		gateway := refOrString(binding.Spec["gateway"])
		if len(gateways) > 0 && !gateways[gateway] && !gateways[lastRef(gateway)] {
			continue
		}
		if len(include) > 0 && !include[binding.Address] && !include[binding.Name] && !include[operation.Address] && !include[operation.Name] {
			continue
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		if httpSpec == nil {
			continue
		}
		switch stringValue(httpSpec["guarantee"]) {
		case "implementation_declared", "opaque", "advisory":
			continue
		}
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func exportedOperations(resources []Resource) (map[string]bool, map[string]bool) {
	exported, declared := map[string]bool{}, map[string]bool{}
	for _, resource := range resources {
		if resource.Kind != "scenery.module" {
			continue
		}
		values, exists := resource.Spec["exports"]
		if !exists {
			continue
		}
		declared[resource.Name] = true
		walkRefs(values, "/exports", func(_ string, reference string) {
			parts := strings.Split(reference, "/")
			if len(parts) == 3 && parts[0] == resource.Name && parts[1] == "operation" {
				exported[reference] = true
				return
			}
			parts = strings.Split(reference, ".")
			if len(parts) >= 2 && parts[0] == "operation" {
				exported[resourceAddress(resource.Name, "operation", parts[1])] = true
			}
		})
	}
	return exported, declared
}

func tsOperationFieldType(operation Resource, resources []Resource, target any) any {
	targetName := lastRef(refOrString(target))
	parts := strings.Split(refString(operation.Spec["input"]), ".")
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

func tsType(value any) string {
	if ref := refString(value); ref != "" {
		for _, segment := range []string{"/record/", "/enum/", "/union/"} {
			if index := strings.LastIndex(ref, segment); index >= 0 {
				return goName(ref[index+len(segment):])
			}
		}
		parts := strings.Split(ref, ".")
		if len(parts) >= 2 && (parts[0] == "record" || parts[0] == "enum" || parts[0] == "union") {
			return goName(parts[1])
		}
		primitive := map[string]string{
			"std.type.problem": "Problem", "std.type.unit": "Unit", "std.type.execution_receipt": "EnqueueReceipt",
			"bool": "boolean", "int": "bigint", "int64": "bigint", "uint64": "bigint", "size": "bigint",
			"int32": "number", "uint32": "number", "float32": "number", "float64": "number", "decimal": "DecimalString",
			"string": "string", "bytes": "Uint8Array", "uuid": "UUIDString", "date": "DateString",
			"datetime": "DateTimeString", "duration": "DurationString", "url": "URLString", "relative_path": "RelativePathString", "json": "JsonValue",
		}
		if name := primitive[ref]; name != "" {
			return name
		}
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return tsTypeExpression(raw)
		}
	}
	return "unknown"
}

func tsTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, wrapper := range []struct{ prefix, before, after string }{
		{"optional(", "", ""}, {"nullable(", "", " | null"}, {"list(", "readonly ", "[]"},
		{"set(", "readonly ", "[]"}, {"map(", "Readonly<Record<string, ", ">>"},
	} {
		if strings.HasPrefix(raw, wrapper.prefix) && strings.HasSuffix(raw, ")") {
			return wrapper.before + tsTypeExpression(strings.TrimSuffix(strings.TrimPrefix(raw, wrapper.prefix), ")")) + wrapper.after
		}
	}
	return tsType(map[string]any{"$ref": raw})
}

func tsName(value string) string {
	name := goName(value)
	if name == "" {
		return ""
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func goType(value any) string {
	if ref := refString(value); ref != "" {
		for _, segment := range []string{"/record/", "/enum/", "/union/"} {
			if index := strings.LastIndex(ref, segment); index >= 0 {
				return goName(ref[index+len(segment):])
			}
		}
		parts := strings.Split(ref, ".")
		if len(parts) >= 2 {
			switch parts[0] {
			case "record", "enum", "union":
				return goName(parts[1])
			}
		}
		primitive := map[string]string{
			"std.type.problem": "scenery.Problem", "std.type.unit": "scenery.Unit",
			"string": "string", "bool": "bool", "int": "scenery.Int", "int32": "int32",
			"int64": "int64", "uint32": "uint32", "uint64": "uint64", "decimal": "scenery.Decimal",
			"float32": "float32", "float64": "float64", "bytes": "[]byte", "uuid": "scenery.UUID",
			"date": "scenery.Date", "datetime": "scenery.DateTime", "duration": "scenery.Duration",
			"size": "scenery.Size", "url": "scenery.URL", "relative_path": "scenery.RelativePath", "json": "scenery.JSON",
		}
		if name := primitive[ref]; name != "" {
			return name
		}
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return goTypeExpression(raw)
		}
	}
	return "any"
}

func goTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == `resource_ref("secret")` {
		return "scenery.SecretRef"
	}
	if strings.HasPrefix(raw, "tuple(") && strings.HasSuffix(raw, ")") {
		canonical := canonicalContractTypeExpression(raw)
		sum := sha256.Sum256([]byte(canonical))
		return "Tuple" + strings.ToUpper(hex.EncodeToString(sum[:8]))
	}
	for _, wrapper := range []struct{ prefix, goPrefix string }{
		{"optional(", "scenery.Optional["}, {"nullable(", "scenery.Nullable["}, {"list(", "[]"},
		{"set(", "scenery.Set["}, {"map(", "map[string]"},
	} {
		if strings.HasPrefix(raw, wrapper.prefix) && strings.HasSuffix(raw, ")") {
			inner := goTypeExpression(strings.TrimSuffix(strings.TrimPrefix(raw, wrapper.prefix), ")"))
			if strings.HasSuffix(wrapper.goPrefix, "[") {
				return wrapper.goPrefix + inner + "]"
			}
			return wrapper.goPrefix + inner
		}
	}
	return goType(map[string]any{"$ref": raw})
}

func canonicalContractTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	open := strings.IndexByte(raw, '(')
	if open < 0 || !strings.HasSuffix(raw, ")") {
		return raw
	}
	name := strings.TrimSpace(raw[:open])
	arguments := splitTypeArguments(raw[open+1 : len(raw)-1])
	for index := range arguments {
		arguments[index] = canonicalContractTypeExpression(arguments[index])
	}
	return name + "(" + strings.Join(arguments, ",") + ")"
}

func eventEmissionKeyExpression(resources map[string]Resource, operation Resource, variantName string, value any) (string, string, error) {
	if value == nil {
		return "", "", nil
	}
	parts := strings.Split(refOrString(value), ".")
	if len(parts) < 3 || parts[0] != "result" || parts[1] != variantName {
		return "", "", fmt.Errorf("must reference a field of result.%s", variantName)
	}
	var resultType any
	for _, variant := range namedChildren(operation.Spec, "result") {
		if stringValue(variant["name"]) == variantName {
			resultType = variant["type"]
			break
		}
	}
	keyType := recordFieldType(resources, operation.Module, resultType, parts[2:])
	if keyType == nil {
		return "", "", fmt.Errorf("references an unknown result field")
	}
	expression := typeExpression(keyType)
	if !eventKeyTypeSupported(expression) {
		return "", "", fmt.Errorf("must reference a supported non-null scalar key, got %s", expression)
	}
	return strings.Join(parts[2:], "."), expression, nil
}

func eventKeyTypeSupported(expression string) bool {
	if strings.HasPrefix(expression, "enum.") || strings.Contains(expression, "/enum/") {
		return true
	}
	switch expression {
	case "bool", "int", "int32", "uint32", "int64", "uint64", "decimal", "float32", "float64", "string", "uuid", "date", "datetime", "duration", "size", "url", "relative_path":
		return true
	default:
		return false
	}
}

func namedChildren(values map[string]any, name string) []map[string]any {
	children := orderedChildren(values, name)
	sort.Slice(children, func(i, j int) bool { return stringValue(children[i]["name"]) < stringValue(children[j]["name"]) })
	return children
}

func orderedChildren(values map[string]any, name string) []map[string]any {
	switch typed := values[name].(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		children := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if child, ok := item.(map[string]any); ok {
				children = append(children, child)
			}
		}
		return children
	default:
		return nil
	}
}

func escapeJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func semanticEqual(left, right any) bool {
	left, right = normalizeComparableScalar(left, right), normalizeComparableScalar(right, left)
	a, aErr := spec.MarshalCanonical(left)
	b, bErr := spec.MarshalCanonical(right)
	return aErr == nil && bErr == nil && string(a) == string(b)
}

func normalizeComparableScalar(value, exemplar any) any {
	scalar, ok := exemplar.(map[string]any)
	if !ok {
		return value
	}
	kind := stringValue(scalar["$scalar"])
	text, textOK := value.(string)
	if kind == "int" && !textOK {
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
			text, textOK = fmt.Sprint(value), true
		}
	}
	if !textOK || !scn.IsContextualPrimitive(kind) && kind != "int" {
		return value
	}
	if kind == "int" {
		return scn.ExactNumericScalar(text)
	}
	converted, err := scn.ContextualizePrimitive(text, kind)
	if err != nil {
		return value
	}
	return converted
}

func typeExpression(value any) string {
	if ref := refString(value); ref != "" {
		return ref
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	return fmt.Sprint(value)
}

func wrappedType(value, wrapper string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, wrapper+"(") && strings.HasSuffix(value, ")")
}

func isOptionalType(value any) bool { return wrappedType(typeExpression(value), "optional") }

func hasTypeWrapper(value, wrapper string) bool {
	value = strings.TrimSpace(value)
	for {
		matched := false
		for _, candidate := range []string{"optional", "nullable", "list", "set", "map"} {
			prefix := candidate + "("
			if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, ")") {
				continue
			}
			if candidate == wrapper {
				return true
			}
			value = strings.TrimSpace(value[len(prefix) : len(value)-1])
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
}
