package generate

import (
	"fmt"
	"math/big"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/spec"
)

var (
	httpPathParameterPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
	httpPathTailPattern      = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\.\.\.\}`)
)

const openAPIVersion = "3.1.1"

var primitiveTypes = map[string]bool{
	"bool": true, "int": true, "int32": true, "int64": true,
	"uint32": true, "uint64": true, "decimal": true, "float32": true,
	"float64": true, "string": true, "bytes": true, "uuid": true,
	"date": true, "datetime": true, "duration": true, "size": true,
	"url": true, "relative_path": true, "json": true,
}

func isCanonicalSHA256Digest(value string) bool { return graph.IsCanonicalSHA256Digest(value) }

func pathWithin(root, path string) bool { return scn.PathWithin(root, path) }

func walkRefs(value any, path string, visit func(string, string)) {
	graph.WalkReferences(value, path, visit)
}

func contractResourceProjection(resource Resource) (Resource, bool) {
	return graph.ContractResourceProjection(resource)
}

func firstError(diagnostics []Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return diagnostic.Code + ": " + diagnostic.Message
		}
	}
	return "unknown error"
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

func nonNegativeInteger(value any) (int64, bool) {
	if value == nil {
		return 0, false
	}
	rational, ok := new(big.Rat).SetString(stringValue(value))
	if !ok || !rational.IsInt() || rational.Sign() < 0 || !rational.Num().IsInt64() {
		return 0, false
	}
	return rational.Num().Int64(), true
}

func forbiddenWorkspacePath(path string) bool {
	for segment := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		switch segment {
		case ".git", ".hg", ".svn", "node_modules", ".scenery":
			return true
		}
	}
	return false
}

func projectionRevisionsForGateways(result *Result, references []string, revisions map[string]string) map[string]string {
	selected := map[string]string{}
	if result == nil || result.Manifest == nil {
		return selected
	}
	for _, reference := range references {
		address := resolveResourceRef(Resource{Module: "app"}, reference, "http_gateway")
		if revision := revisions[address]; revision != "" {
			selected[address] = revision
		}
	}
	return selected
}

func httpBindingsForGateway(resources []Resource, gateway Resource) []Resource {
	var bindings []Resource
	for _, binding := range resources {
		if binding.Kind == "scenery.binding" && stringValue(binding.Spec["protocol"]) == "http" && resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway") == gateway.Address {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func inputKeyFieldName(value any) (string, bool) {
	reference, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	expression := strings.TrimSpace(stringValue(reference["$expression"]))
	if expression == "" {
		expression = strings.TrimSpace(stringValue(reference["$ref"]))
	}
	name, found := strings.CutPrefix(expression, "input.")
	return name, found && scn.IdentifierPattern.MatchString(name)
}

func literalString(block *Block, name string) (string, bool) { return scn.LiteralString(block, name) }

func MarshalCanonical(value any) ([]byte, error) { return spec.MarshalCanonical(value) }

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

func literalStringList(block *Block, name string) []string {
	expression, ok := block.Attributes[name]
	if !ok {
		return nil
	}
	values, ok := expression.Value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			result = append(result, item)
		}
	}
	return result
}

func refOrString(value any) string {
	if reference := refString(value); reference != "" {
		return reference
	}
	return stringValue(value)
}

func expressionText(value any) string {
	if expression, ok := value.(map[string]any); ok {
		return strings.TrimSpace(stringValue(expression["$expression"]))
	}
	return strings.TrimSpace(stringValue(value))
}

func wrappedType(value, wrapper string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, wrapper+"(") && strings.HasSuffix(value, ")")
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

func recordFieldType(resources map[string]Resource, module string, value any, path []string) any {
	current := value
	for _, name := range path {
		record, ok := recordResourceForType(resources, module, current)
		if !ok {
			return nil
		}
		module, current = record.Module, nil
		for _, field := range namedChildren(record.Spec, "field") {
			if stringValue(field["name"]) == name {
				current = field["type"]
				break
			}
		}
		if current == nil {
			return nil
		}
	}
	return current
}

func recordResourceForType(resources map[string]Resource, module string, value any) (Resource, bool) {
	reference := refString(value)
	if strings.Contains(reference, "/record/") {
		record, ok := resources[reference]
		return record, ok
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 || parts[0] != "record" {
		return Resource{}, false
	}
	record, ok := resources[graph.ResourceAddress(module, "record", parts[1])]
	return record, ok
}

func validateHTTPOutcomeValueRef(resources map[string]Resource, operation Resource, outcome, reference string) error {
	if reference == "" {
		return fmt.Errorf("requires a typed from reference")
	}
	if !strings.HasPrefix(outcome, "result.") && !strings.HasPrefix(outcome, "error.") {
		if outcome == "dispatch.enqueued" {
			parts := strings.Split(reference, ".")
			if reference == "dispatch.receipt" || len(parts) == 3 && parts[0] == "dispatch" && parts[1] == "receipt" && oneOf(parts[2], "durable_identity", "execution_id", "accepted_revision", "status_url") {
				return nil
			}
			return fmt.Errorf("from reference does not match dispatch.enqueued receipt")
		}
		problem := standardProblemSource(outcome)
		parts := strings.Split(reference, ".")
		if reference == problem || strings.HasPrefix(reference, problem+".") && len(parts) == 3 && oneOf(parts[2], "code", "message", "path") {
			return nil
		}
		return fmt.Errorf("from reference does not match %s", outcome)
	}
	parts, outcomeParts := strings.Split(reference, "."), strings.Split(outcome, ".")
	if len(parts) < 2 || parts[0] != outcomeParts[0] || parts[1] != outcomeParts[1] {
		return fmt.Errorf("from reference does not match %s", outcome)
	}
	if len(parts) == 2 {
		return nil
	}
	variantType := operationVariantType(operation, outcomeParts[0], outcomeParts[1])
	if refOrString(variantType) == "std.type.problem" {
		if len(parts) == 3 && oneOf(parts[2], "code", "message", "path") {
			return nil
		}
		return fmt.Errorf("references an unknown problem field")
	}
	if recordFieldType(resources, operation.Module, variantType, parts[2:]) != nil {
		return nil
	}
	return fmt.Errorf("references an unknown outcome field")
}

func standardProblemSource(outcome string) string {
	if lastRef(outcome) == "enqueued" {
		return "dispatch.receipt"
	}
	if outcome == "system.internal" {
		return "system.problem"
	}
	if strings.HasPrefix(outcome, "admission.") {
		return "admission.problem"
	}
	if strings.HasPrefix(outcome, "dispatch.") {
		return "dispatch.problem"
	}
	return "transport.problem"
}

func joinHTTPPath(base, binding string) string {
	if base == "" || base == "/" {
		return binding
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(binding, "/")
}

func oneOf[T comparable](value T, candidates ...T) bool {
	return slices.Contains(candidates, value)
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
	return append(parts, strings.TrimSpace(value[start:]))
}
