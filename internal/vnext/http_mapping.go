package vnext

import (
	"fmt"
	"mime"
	"regexp"
	"sort"
	"strings"
)

var httpHeaderNamePattern = regexp.MustCompile(`^[a-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)

type operationInputField struct {
	Name       string
	WireName   string
	Type       any
	Optional   bool
	HasDefault bool
	EnumValues []string
}

type operationInputShape struct {
	Type   any
	Unit   bool
	Record *Resource
	Fields map[string]operationInputField
}

func validateHTTPInputMappings(resources map[string]Resource, binding, operation Resource, httpSpec map[string]any) []Diagnostic {
	shape := resolveOperationInputShape(resources, operation)
	counts := map[string]int{}
	var diagnostics []Diagnostic

	addTarget := func(source string, mapping map[string]any, scalarOnly bool) {
		target := refOrString(mapping["to"])
		field, whole, ok := resolveOperationInputTarget(operation, shape, target)
		if !ok || whole {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: fmt.Sprintf("%s mapping has invalid target %q", source, target), Address: binding.Address})
			return
		}
		counts[field.Name]++
		if scalarOnly && !httpPathScalarType(field.Type, resources, operation.Module) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: fmt.Sprintf("%s mapping target %s is not a scalar with a supported wire codec", source, field.Name), Address: binding.Address})
		}
		if source != "path_parameter" && !httpMappedTypeSupported(field.Type, stringValue(mapping["encoding"]), resources, operation.Module) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: fmt.Sprintf("%s mapping target %s requires an explicit supported encoding", source, field.Name), Address: binding.Address})
		}
	}

	for _, mapping := range namedChildren(httpSpec, "path_parameter") {
		addTarget("path_parameter", mapping, true)
	}
	for _, source := range []string{"query_parameter", "header", "cookie"} {
		seenNames := map[string]bool{}
		for _, mapping := range namedChildren(httpSpec, source) {
			name := stringValue(mapping["name"])
			if name == "" || seenNames[name] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2115", Severity: "error", Message: source + " mappings require unique non-empty wire names", Address: binding.Address})
			}
			seenNames[name] = true
			if source == "header" && (name != strings.ToLower(name) || !httpHeaderNamePattern.MatchString(name)) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2115", Severity: "error", Message: "HTTP header mapping names must be canonical lower-case field names", Address: binding.Address})
			}
			addTarget(source, mapping, source == "cookie")
		}
	}
	for _, mapping := range namedChildren(httpSpec, "context") {
		from := refOrString(mapping["from"])
		if !strings.HasPrefix(from, "principal.") && !strings.HasPrefix(from, "context.") {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2115", Severity: "error", Message: "context mappings may read only principal or typed request context fields", Address: binding.Address})
		}
		addTarget("context", mapping, false)
	}

	if body, _ := httpSpec["body"].(map[string]any); body != nil {
		target := refOrString(body["to"])
		field, whole, ok := resolveOperationInputTarget(operation, shape, target)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: fmt.Sprintf("body mapping has invalid target %q", target), Address: binding.Address})
		} else if whole {
			include := httpBodyFieldSelection(body, "include", operation, shape)
			except := httpBodyFieldSelection(body, "except", operation, shape)
			if len(include.invalid) > 0 || len(except.invalid) > 0 {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "body include/except contains an invalid operation input field target", Address: binding.Address})
			}
			if include.present && except.present {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "body include and except are mutually exclusive", Address: binding.Address})
			}
			if shape.Record == nil && (include.present || except.present) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "body include/except requires a record operation input", Address: binding.Address})
			}
			if shape.Record == nil {
				counts[""]++
			} else {
				for name := range shape.Fields {
					selected := true
					if include.present {
						selected = include.fields[name]
					}
					if except.fields[name] {
						selected = false
					}
					if selected {
						counts[name]++
					}
				}
			}
		} else {
			counts[field.Name]++
			if body["include"] != nil || body["except"] != nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "body include/except is valid only for an entire record input", Address: binding.Address})
			}
		}
		if !httpBodyCodecSupports(stringValue(body["codec"]), shape, field, whole) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "body codec is incompatible with its target", Address: binding.Address})
		}
		diagnostics = append(diagnostics, validateHTTPBodyDetails(resources, binding, operation, shape, body, whole)...)
	}

	if shape.Record == nil {
		if shape.Unit {
			if len(counts) != 0 {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "unit operation input cannot be populated by HTTP mappings", Address: binding.Address})
			}
			return diagnostics
		}
		if counts[""] != 1 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "non-record operation input must be populated exactly once", Address: binding.Address})
		}
		return diagnostics
	}
	for name, field := range shape.Fields {
		if counts[name] > 1 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: fmt.Sprintf("operation input field %s is populated more than once", name), Address: binding.Address})
		}
		if counts[name] == 0 && !field.Optional && !field.HasDefault {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: fmt.Sprintf("required operation input field %s is not populated", name), Address: binding.Address})
		}
	}
	return diagnostics
}

func validateHTTPBodyDetails(resources map[string]Resource, binding, operation Resource, shape operationInputShape, body map[string]any, whole bool) []Diagnostic {
	codec := stringValue(body["codec"])
	if codec != "form" && codec != "multipart" {
		return nil
	}
	if !whole || shape.Record == nil {
		return []Diagnostic{{Code: "SCN2116", Severity: "error", Message: codec + " body requires an entire record input", Address: binding.Address}}
	}
	if codec == "form" {
		if len(namedChildren(body, "part")) > 0 {
			return []Diagnostic{{Code: "SCN2116", Severity: "error", Message: "form body cannot declare multipart parts", Address: binding.Address}}
		}
		return nil
	}
	seenNames, seenFields := map[string]bool{}, map[string]bool{}
	var diagnostics []Diagnostic
	for _, part := range namedChildren(body, "part") {
		name := stringValue(part["name"])
		field, targetWhole, valid := resolveOperationInputTarget(operation, shape, refOrString(part["to"]))
		if name == "" || seenNames[name] || !valid || targetWhole || seenFields[field.Name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2117", Severity: "error", Message: "multipart parts require unique names and input field targets", Address: binding.Address})
			continue
		}
		seenNames[name], seenFields[field.Name] = true, true
		kind := stringValue(part["kind"])
		typeName := unwrapHTTPType(typeExpression(field.Type))
		multiple := part["multiple"] == true
		if multiple {
			if !strings.HasPrefix(typeName, "list(") && !strings.HasPrefix(typeName, "set(") {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2118", Severity: "error", Message: "multiple multipart part requires a list or set target", Address: binding.Address})
			}
			typeName = strings.TrimSpace(typeName[strings.IndexByte(typeName, '(')+1 : len(typeName)-1])
		}
		mediaTypes := literalStringListFromValue(part["media_types"])
		seenMedia, concreteMedia := map[string]bool{}, false
		for _, value := range mediaTypes {
			mediaType, _, err := mime.ParseMediaType(value)
			if err != nil || mediaType == "" || seenMedia[value] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "multipart media_types must be valid and unique", Address: binding.Address})
				continue
			}
			seenMedia[value] = true
			if !strings.Contains(mediaType, "*") {
				concreteMedia = true
			}
		}
		switch kind {
		case "text":
			if typeName == "bytes" || typeName == "json" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2118", Severity: "error", Message: "text multipart part target has no text scalar codec", Address: binding.Address})
			}
		case "bytes":
			if typeName != "bytes" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2118", Severity: "error", Message: "bytes multipart part requires a bytes target", Address: binding.Address})
			}
		case "file":
			if typeName != "bytes" && !strings.HasPrefix(typeName, "record.") {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2118", Severity: "error", Message: "file multipart part requires bytes or a declared file record", Address: binding.Address})
			}
			if len(mediaTypes) == 0 {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "file multipart part requires accepted media_types", Address: binding.Address})
			}
			retainFilename := part["retain_filename"] == true
			if typeName == "bytes" && retainFilename {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "retained file metadata requires a file record target", Address: binding.Address})
			}
			if typeName == "bytes" && !concreteMedia {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "byte-only file targets require at least one concrete accepted media type", Address: binding.Address})
			}
			if strings.HasPrefix(typeName, "record.") {
				if !retainFilename || !validMultipartFileRecord(resources, operation.Module, typeName) {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "file record targets require retained bytes, filename, and media_type fields", Address: binding.Address})
				}
			}
		default:
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2118", Severity: "error", Message: "multipart part kind must be text, bytes, or file", Address: binding.Address})
		}
		if part["retain_filename"] == true && kind != "file" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "retain_filename is valid only for file parts", Address: binding.Address})
		}
		if value, ok := integerValue(part["max_bytes"]); part["max_bytes"] != nil && (!ok || value <= 0) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2119", Severity: "error", Message: "multipart max_bytes must be positive", Address: binding.Address})
		}
	}
	for name, field := range shape.Fields {
		if !field.Optional && !seenFields[name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2117", Severity: "error", Message: "required multipart input field " + name + " has no declared part", Address: binding.Address})
		}
	}
	return diagnostics
}

func validMultipartFileRecord(resources map[string]Resource, module, typeName string) bool {
	record, ok := recordResourceForType(resources, module, map[string]any{"$ref": typeName})
	if !ok {
		return false
	}
	want := map[string]string{"bytes": "bytes", "filename": "string", "media_type": "string"}
	for _, field := range namedChildren(record.Spec, "field") {
		name := stringValue(field["name"])
		if expected, required := want[name]; required {
			if unwrapHTTPType(typeExpression(field["type"])) != expected || isOptionalType(field["type"]) {
				return false
			}
			delete(want, name)
		} else if !isOptionalType(field["type"]) {
			return false
		}
	}
	return len(want) == 0
}

func resolveOperationInputShape(resources map[string]Resource, operation Resource) operationInputShape {
	shape := operationInputShape{Type: operation.Spec["input"], Fields: map[string]operationInputField{}}
	reference := refString(shape.Type)
	if reference == "std.type.unit" {
		shape.Unit = true
		return shape
	}
	record, ok := resources[resolveResourceRef(operation, reference, "record")]
	if !ok {
		return shape
	}
	shape.Record = &record
	for _, field := range namedChildren(record.Spec, "field") {
		name := stringValue(field["name"])
		shape.Fields[name] = operationInputField{
			Name: name, WireName: wireName(field, name), Type: field["type"], Optional: isOptionalType(field["type"]), HasDefault: field["default"] != nil,
			EnumValues: enumWireValues(resources, operation.Module, field["type"]),
		}
	}
	return shape
}

func resolveOperationInputTarget(operation Resource, shape operationInputShape, target string) (operationInputField, bool, bool) {
	prefix := "operation." + operation.Name + ".input"
	if target == prefix {
		return operationInputField{}, true, true
	}
	fieldName, found := strings.CutPrefix(target, prefix+".")
	if !found || strings.Contains(fieldName, ".") {
		return operationInputField{}, false, false
	}
	field, ok := shape.Fields[fieldName]
	return field, false, ok
}

type httpFieldSelection struct {
	present bool
	fields  map[string]bool
	invalid []string
}

func httpBodyFieldSelection(body map[string]any, key string, operation Resource, shape operationInputShape) httpFieldSelection {
	selection := httpFieldSelection{fields: map[string]bool{}}
	value, exists := body[key]
	if !exists {
		return selection
	}
	selection.present = true
	values, ok := value.([]any)
	if !ok {
		selection.invalid = append(selection.invalid, fmt.Sprint(value))
		return selection
	}
	for _, item := range values {
		field, whole, valid := resolveOperationInputTarget(operation, shape, refOrString(item))
		if !valid || whole || selection.fields[field.Name] {
			selection.invalid = append(selection.invalid, refOrString(item))
			continue
		}
		selection.fields[field.Name] = true
	}
	return selection
}

func httpPathScalarType(value any, resources map[string]Resource, module string) bool {
	expression := unwrapHTTPType(typeExpression(value))
	if strings.HasPrefix(expression, "list(") || strings.HasPrefix(expression, "set(") || strings.HasPrefix(expression, "map(") || strings.HasPrefix(expression, "tuple(") {
		return false
	}
	if strings.HasPrefix(expression, "enum.") {
		_, ok := resources[resourceAddress(module, "enum", strings.TrimPrefix(expression, "enum."))]
		return ok
	}
	return primitiveTypes[expression] && expression != "json" && expression != "bytes"
}

func httpMappedTypeSupported(value any, encoding string, resources map[string]Resource, module string) bool {
	if hasTypeWrapper(typeExpression(value), "nullable") && encoding != "json" {
		return false
	}
	expression := unwrapHTTPType(typeExpression(value))
	if encoding == "json" {
		return true
	}
	if strings.HasPrefix(expression, "list(") || strings.HasPrefix(expression, "set(") {
		inner := strings.TrimSpace(expression[strings.IndexByte(expression, '(')+1 : len(expression)-1])
		if encoding == "comma" {
			return httpCommaScalarTypeSupported(map[string]any{"$ref": inner}, resources, module)
		}
		return (encoding == "" || encoding == "repeated") && httpPathScalarType(map[string]any{"$ref": inner}, resources, module)
	}
	return (encoding == "" || encoding == "repeated") && httpPathScalarType(map[string]any{"$ref": expression}, resources, module)
}

func httpCommaScalarTypeSupported(value any, resources map[string]Resource, module string) bool {
	expression := unwrapHTTPType(typeExpression(value))
	if strings.HasPrefix(expression, "enum.") {
		enum, ok := resources[resourceAddress(module, "enum", strings.TrimPrefix(expression, "enum."))]
		if !ok {
			return false
		}
		for _, item := range namedChildren(enum.Spec, "value") {
			if strings.Contains(wireName(item, stringValue(item["name"])), ",") {
				return false
			}
		}
		return true
	}
	return map[string]bool{
		"bool": true, "int": true, "int32": true, "int64": true, "uint32": true, "uint64": true,
		"decimal": true, "float32": true, "float64": true, "uuid": true, "date": true,
		"datetime": true, "duration": true, "size": true,
	}[expression]
}

func httpBodyCodecSupports(codec string, shape operationInputShape, field operationInputField, whole bool) bool {
	targetType := shape.Type
	if !whole {
		targetType = field.Type
	}
	expression := unwrapHTTPType(typeExpression(targetType))
	switch codec {
	case "json":
		return expression != "std.type.problem"
	case "problem_json":
		return expression == "std.type.problem"
	case "text":
		return expression == "string" || strings.HasPrefix(expression, "enum.")
	case "bytes":
		return expression == "bytes"
	case "form", "multipart":
		return whole && shape.Record != nil
	default:
		return false
	}
}

func unwrapHTTPType(expression string) string {
	expression = strings.TrimSpace(expression)
	for _, wrapper := range []string{"optional", "nullable"} {
		for strings.HasPrefix(expression, wrapper+"(") && strings.HasSuffix(expression, ")") {
			expression = strings.TrimSpace(expression[len(wrapper)+1 : len(expression)-1])
		}
	}
	return expression
}

func enumWireValues(resources map[string]Resource, module string, value any) []string {
	expression := unwrapHTTPType(typeExpression(value))
	for _, wrapper := range []string{"list", "set"} {
		if strings.HasPrefix(expression, wrapper+"(") && strings.HasSuffix(expression, ")") {
			expression = strings.TrimSpace(expression[len(wrapper)+1 : len(expression)-1])
		}
	}
	if !strings.HasPrefix(expression, "enum.") {
		return nil
	}
	enum, ok := resources[resourceAddress(module, "enum", strings.TrimPrefix(expression, "enum."))]
	if !ok || enum.Spec["open"] == true {
		return nil
	}
	var values []string
	for _, value := range namedChildren(enum.Spec, "value") {
		values = append(values, wireName(value, stringValue(value["name"])))
	}
	sort.Strings(values)
	return values
}
