package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func LoadManifestReference(reference string) (*Manifest, error) {
	info, err := os.Stat(reference)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		result, compileErr := Compile(reference)
		if compileErr != nil {
			return nil, compileErr
		}
		if !result.Valid() {
			return nil, fmt.Errorf("%s does not compile to a valid manifest", reference)
		}
		return result.Manifest, nil
	}
	b, err := os.ReadFile(filepath.Clean(reference))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(b, &manifest); err == nil && manifest.APIVersion == ManifestVersion {
		return &manifest, nil
	}
	var envelope struct {
		Data struct {
			Manifest *Manifest `json:"manifest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil || envelope.Data.Manifest == nil {
		return nil, fmt.Errorf("%s is not a scenery manifest or compile envelope", reference)
	}
	return envelope.Data.Manifest, nil
}

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
	a, aErr := MarshalCanonical(left)
	b, bErr := MarshalCanonical(right)
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
	if !textOK || !contextualPrimitiveTypes[kind] && kind != "int" {
		return value
	}
	if kind == "int" {
		return exactNumericScalar(text)
	}
	converted, err := contextualizeValue(text, kind, "app", nil)
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
	b, _ := MarshalCanonical(diff)
	sum := sha256.Sum256(append([]byte("scenery.semantic-diff.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compatibilityCatalogDigest() string {
	projection := map[string]any{"profile": "scenery.compatibility-core/v1", "revision": "0.4-draft", "dimensions": compatibilityDimensions}
	b, _ := MarshalCanonical(projection)
	sum := sha256.Sum256(append([]byte("scenery.compatibility-catalog.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func resourceSchemaRevision(kind string) string {
	schema, ok := CoreSchema(kind)
	if !ok {
		return ""
	}
	b, _ := MarshalCanonical(schema)
	sum := sha256.Sum256(append([]byte("scenery.resource-schema-revision.v1\x00"), b...))
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
	return false
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
	a, _ := MarshalCanonical(left)
	b, _ := MarshalCanonical(right)
	return string(a) < string(b)
}
