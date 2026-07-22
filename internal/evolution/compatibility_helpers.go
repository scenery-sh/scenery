package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"scenery.sh/internal/scn"
	"scenery.sh/internal/spec"
)

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	out := map[string]Resource{}
	if manifest == nil {
		return out
	}
	for _, resource := range manifest.Resources {
		out[resource.Address] = resource
	}
	return out
}

func stringUnion[A any, B any](left map[string]A, right map[string]B) []string {
	set := map[string]bool{}
	for key := range left {
		set[key] = true
	}
	for key := range right {
		set[key] = true
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	contextual := scn.IsContextualPrimitive(kind)
	if !textOK || !contextual && kind != "int" {
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

func strongestClassification(classifications map[string]Classification) string {
	strongest := CompatibilityCompatible
	for _, classification := range classifications {
		if !classification.Applicable {
			continue
		}
		switch classification.Result {
		case CompatibilityBreaking:
			return CompatibilityBreaking
		case CompatibilityMigrationRequired:
			strongest = CompatibilityMigrationRequired
		case CompatibilityUnknown:
			if strongest == CompatibilityCompatible {
				strongest = CompatibilityUnknown
			}
		}
	}
	return strongest
}

func stableChangeID(change SemanticChange) string {
	sum := sha256.Sum256([]byte(change.Operation + "\x00" + change.Address + "\x00" + change.Path))
	return "chg_" + hex.EncodeToString(sum[:8])
}

func semanticDiffDigest(diff SemanticDiff) string {
	diff.Digest = ""
	b, _ := spec.MarshalCanonical(diff)
	sum := sha256.Sum256(append([]byte("scenery.semantic-diff\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compatibilityCatalogDigest() string {
	projection := map[string]any{"revision": "0.4-draft", "dimensions": compatibilityDimensions}
	b, _ := spec.MarshalCanonical(projection)
	sum := sha256.Sum256(append([]byte("scenery.compatibility-catalog\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func resourceSchemaRevision(kind string) string {
	schema, ok := spec.CoreSchema(kind)
	if !ok {
		return ""
	}
	b, _ := spec.MarshalCanonical(schema)
	sum := sha256.Sum256(append([]byte("scenery.resource-schema-revision\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func namedChildAtPath(path, child string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 3 && parts[0] == "spec" && parts[1] == child {
		return parts[2], true
	}
	return "", false
}

func isOptionalType(value any) bool {
	return wrappedType(typeExpression(value), "optional")
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

func innermostType(value string) string {
	value = strings.TrimSpace(value)
	for _, wrapper := range []string{"optional", "nullable", "list", "set", "map"} {
		prefix := wrapper + "("
		if strings.HasPrefix(value, prefix) && strings.HasSuffix(value, ")") {
			return innermostType(strings.TrimSpace(value[len(prefix) : len(value)-1]))
		}
	}
	return value
}

func numericWidening(base, target string) bool {
	return base == "int32" && (target == "int64" || target == "int") || base == "int64" && target == "int" || base == "uint32" && target == "uint64"
}

func constraintName(path string) string {
	path = strings.TrimSuffix(path, "/value")
	for _, name := range []string{"minimum", "maximum", "min_length", "max_length", "min_items", "max_items", "pattern"} {
		if strings.HasSuffix(path, "/"+name) {
			return name
		}
	}
	return ""
}

func compareNumbers(base, target any) (int, bool) {
	if base == nil || target == nil {
		return 0, false
	}
	left, ok := new(big.Rat).SetString(fmt.Sprint(base))
	if !ok {
		return 0, false
	}
	right, ok := new(big.Rat).SetString(fmt.Sprint(target))
	if !ok {
		return 0, false
	}
	return left.Cmp(right), true
}

func securityRelevantPath(path string) bool {
	for _, fragment := range []string{"/exposure", "/authentication", "/authorization", "/principal", "/tenant", "/credential", "/sensitive", "/pipeline", "/secret"} {
		if strings.Contains(path, fragment) {
			return true
		}
	}
	return strings.Contains(path, "/path_tail/")
}

func classified(result, rule string) Classification {
	return Classification{Applicable: true, Result: result, Rule: rule}
}

func notApplicable() Classification {
	return Classification{Applicable: false, Rule: "SCN_COMPAT_NOT_APPLICABLE"}
}

func oneOf[T comparable](value T, candidates ...T) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func canonicalLess(left, right any) bool {
	a, _ := spec.MarshalCanonical(left)
	b, _ := spec.MarshalCanonical(right)
	return string(a) < string(b)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func byteDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func namedChildren(parent map[string]any, name string) []map[string]any {
	switch value := parent[name].(type) {
	case map[string]any:
		return []map[string]any{value}
	case []any:
		result := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if child, ok := item.(map[string]any); ok {
				result = append(result, child)
			}
		}
		return result
	default:
		return nil
	}
}

func refString(value any) string {
	object, _ := value.(map[string]any)
	ref, _ := object["$ref"].(string)
	return ref
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
