// Package schemacheck owns the existing current-contract JSON Schema checks used
// by repository verification and product contract tests, never product execution.
package schemacheck

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ValidateFile checks a payload against the repository's supported schema vocabulary.
func ValidateFile(schemaPath string, payload any) []string {
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		return []string{err.Error()}
	}
	var schema any
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return []string{"invalid schema JSON: " + err.Error()}
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return []string{"failed to marshal payload: " + err.Error()}
	}
	var value any
	if err := json.Unmarshal(payloadData, &value); err != nil {
		return []string{"failed to normalize payload JSON: " + err.Error()}
	}
	validator := harnessSchemaValidator{root: schema, schemaDir: filepath.Dir(schemaPath)}
	return validator.validate(schema, value, "$")
}

type harnessSchemaValidator struct {
	root      any
	schemaDir string
}

func (v harnessSchemaValidator) validate(schema any, value any, path string) []string {
	if allowed, ok := schema.(bool); ok {
		if allowed {
			return nil
		}
		return []string{path + ": value is rejected by false schema"}
	}
	node, ok := schema.(map[string]any)
	if !ok {
		return []string{path + ": schema node is not an object"}
	}
	if ref, _ := node["$ref"].(string); ref != "" {
		resolved, validator, err := v.resolveRef(ref)
		if err != nil {
			return []string{path + ": " + err.Error()}
		}
		return validator.validate(resolved, value, path)
	}
	var diagnostics []string
	diagnostics = append(diagnostics, v.validateCompositions(node, value, path)...)
	if constValue, ok := node["const"]; ok && !reflect.DeepEqual(constValue, value) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %s does not equal const %s", path, compactJSON(value), compactJSON(constValue)))
	}
	if enumValues, ok := node["enum"].([]any); ok {
		matched := false
		for _, enumValue := range enumValues {
			if reflect.DeepEqual(enumValue, value) {
				matched = true
				break
			}
		}
		if !matched {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: value %s is not in enum", path, compactJSON(value)))
		}
	}
	types := schemaTypes(node["type"])
	if len(types) > 0 && !jsonValueMatchesAnyType(value, types) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value has type %s, want %s", path, jsonValueType(value), strings.Join(types, "|")))
		return diagnostics
	}
	if jsonValueMatchesAnyType(value, []string{"object"}) {
		diagnostics = append(diagnostics, v.validateObject(node, value.(map[string]any), path)...)
	}
	if jsonValueMatchesAnyType(value, []string{"array"}) {
		diagnostics = append(diagnostics, v.validateArray(node, value.([]any), path)...)
	}
	if text, ok := value.(string); ok {
		diagnostics = append(diagnostics, validateStringConstraints(node, text, path)...)
	}
	diagnostics = append(diagnostics, validateNumericBounds(node, value, path)...)
	return diagnostics
}

func (v harnessSchemaValidator) validateCompositions(schema map[string]any, value any, path string) []string {
	var diagnostics []string
	if alternatives, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, alternative := range alternatives {
			if len(v.validate(alternative, value, path)) == 0 {
				matches++
			}
		}
		if matches != 1 {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: value matches %d oneOf alternatives, want exactly 1", path, matches))
		}
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, alternative := range alternatives {
			if len(v.validate(alternative, value, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			diagnostics = append(diagnostics, path+": value does not match anyOf alternatives")
		}
	}
	if requirements, ok := schema["allOf"].([]any); ok {
		for _, requirement := range requirements {
			diagnostics = append(diagnostics, v.validate(requirement, value, path)...)
		}
	}
	if rejected, ok := schema["not"]; ok && len(v.validate(rejected, value, path)) == 0 {
		diagnostics = append(diagnostics, path+": value matches forbidden not schema")
	}
	return diagnostics
}

func (v harnessSchemaValidator) validateObject(schema map[string]any, value map[string]any, path string) []string {
	var diagnostics []string
	if minimum, ok := schema["minProperties"].(float64); ok && len(value) < int(minimum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: object has %d properties, want at least %d", path, len(value), int(minimum)))
	}
	if required, ok := schema["required"].([]any); ok {
		for _, raw := range required {
			key, _ := raw.(string)
			if key == "" {
				continue
			}
			if _, ok := value[key]; !ok {
				diagnostics = append(diagnostics, path+"."+key+": required property missing")
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	propertyNames := schema["propertyNames"]
	patternProperties, _ := schema["patternProperties"].(map[string]any)
	compiledPatterns := map[string]*regexp.Regexp{}
	for pattern := range patternProperties {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			diagnostics = append(diagnostics, path+": invalid patternProperties expression "+pattern)
			continue
		}
		compiledPatterns[pattern] = compiled
	}
	for key, propertyValue := range value {
		if propertyNames != nil {
			diagnostics = append(diagnostics, v.validate(propertyNames, key, path+".<property-name>")...)
		}
		for pattern, propertySchema := range patternProperties {
			if compiledPatterns[pattern] != nil && compiledPatterns[pattern].MatchString(key) {
				diagnostics = append(diagnostics, v.validate(propertySchema, propertyValue, path+"."+key)...)
			}
		}
	}
	for key, propertySchema := range properties {
		propertyValue, ok := value[key]
		if !ok {
			continue
		}
		diagnostics = append(diagnostics, v.validate(propertySchema, propertyValue, path+"."+key)...)
	}
	if additional, ok := schema["additionalProperties"]; ok {
		switch additional := additional.(type) {
		case bool:
			if !additional {
				for key := range value {
					if _, ok := properties[key]; !ok && !matchesSchemaPattern(key, compiledPatterns) {
						diagnostics = append(diagnostics, path+"."+key+": additional property is not allowed")
					}
				}
			}
		case map[string]any:
			for key, propertyValue := range value {
				if _, ok := properties[key]; ok || matchesSchemaPattern(key, compiledPatterns) {
					continue
				}
				diagnostics = append(diagnostics, v.validate(additional, propertyValue, path+"."+key)...)
			}
		}
	}
	sort.Strings(diagnostics)
	return diagnostics
}

func matchesSchemaPattern(value string, patterns map[string]*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern != nil && pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func (v harnessSchemaValidator) validateArray(schema map[string]any, value []any, path string) []string {
	var diagnostics []string
	if minimum, ok := schema["minItems"].(float64); ok && len(value) < int(minimum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: array has %d items, want at least %d", path, len(value), int(minimum)))
	}
	if maximum, ok := schema["maxItems"].(float64); ok && len(value) > int(maximum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: array has %d items, want at most %d", path, len(value), int(maximum)))
	}
	if unique, _ := schema["uniqueItems"].(bool); unique {
		seen := map[string]bool{}
		for _, item := range value {
			key := compactJSON(item)
			if seen[key] {
				diagnostics = append(diagnostics, path+": array items must be unique")
				break
			}
			seen[key] = true
		}
	}
	if items, ok := schema["items"]; ok {
		for i, item := range value {
			diagnostics = append(diagnostics, v.validate(items, item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return diagnostics
}

func validateStringConstraints(schema map[string]any, value, path string) []string {
	var diagnostics []string
	length := utf8.RuneCountInString(value)
	if minimum, ok := schema["minLength"].(float64); ok && length < int(minimum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: string length %d is less than minimum %d", path, length, int(minimum)))
	}
	if maximum, ok := schema["maxLength"].(float64); ok && length > int(maximum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: string length %d exceeds maximum %d", path, length, int(maximum)))
	}
	if pattern, ok := schema["pattern"].(string); ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			diagnostics = append(diagnostics, path+": schema pattern is invalid")
		} else if !compiled.MatchString(value) {
			diagnostics = append(diagnostics, path+": string does not match pattern "+pattern)
		}
	}
	if format, _ := schema["format"].(string); format == "date-time" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			diagnostics = append(diagnostics, path+": string is not an RFC 3339 date-time")
		}
	}
	if encoding, _ := schema["contentEncoding"].(string); encoding == "base64" {
		if _, err := base64.StdEncoding.DecodeString(value); err != nil {
			diagnostics = append(diagnostics, path+": string is not valid base64")
		}
	}
	return diagnostics
}

func (v harnessSchemaValidator) resolveRef(ref string) (any, harnessSchemaValidator, error) {
	if strings.HasPrefix(ref, "#") {
		return v.resolveLocalRef(ref)
	}
	schemaFile, fragment, _ := strings.Cut(ref, "#")
	if schemaFile == "" {
		return nil, v, fmt.Errorf("unsupported $ref %q", ref)
	}
	if filepath.IsAbs(schemaFile) || strings.Contains(schemaFile, "://") {
		return nil, v, fmt.Errorf("unsupported external $ref %q", ref)
	}
	cleanFile := filepath.Clean(filepath.FromSlash(schemaFile))
	if cleanFile == "." || cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) {
		return nil, v, fmt.Errorf("external $ref %q escapes schema directory", ref)
	}
	schemaPath := filepath.Join(v.schemaDir, cleanFile)
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, v, fmt.Errorf("failed to read external $ref %q: %w", ref, err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, v, fmt.Errorf("invalid external schema %q: %w", ref, err)
	}
	external := harnessSchemaValidator{root: schema, schemaDir: filepath.Dir(schemaPath)}
	if fragment == "" {
		return schema, external, nil
	}
	return external.resolveRef("#" + fragment)
}

func (v harnessSchemaValidator) resolveLocalRef(ref string) (any, harnessSchemaValidator, error) {
	const prefix = "#/$defs/"
	if ref == "#" {
		return v.root, v, nil
	}
	if !strings.HasPrefix(ref, prefix) {
		return nil, v, fmt.Errorf("unsupported $ref %q", ref)
	}
	root, ok := v.root.(map[string]any)
	if !ok {
		return nil, v, fmt.Errorf("schema root is not an object")
	}
	defs, ok := root["$defs"].(map[string]any)
	if !ok {
		return nil, v, fmt.Errorf("schema has no $defs for %q", ref)
	}
	key := strings.TrimPrefix(ref, prefix)
	value, ok := defs[key]
	if !ok {
		return nil, v, fmt.Errorf("schema $defs missing %q", key)
	}
	return value, v, nil
}

func schemaTypes(raw any) []string {
	switch raw := raw.(type) {
	case string:
		return []string{raw}
	case []any:
		var out []string
		for _, value := range raw {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func jsonValueMatchesAnyType(value any, types []string) bool {
	for _, typ := range types {
		switch typ {
		case "null":
			if value == nil {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "number":
			if _, ok := value.(float64); ok {
				return true
			}
		case "integer":
			if number, ok := value.(float64); ok && math.Trunc(number) == number {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		}
	}
	return false
}

func jsonValueType(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case float64:
		if math.Trunc(value) == value {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func validateNumericBounds(schema map[string]any, value any, path string) []string {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	var diagnostics []string
	if min, ok := schema["minimum"].(float64); ok && number < min {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %.3f is less than minimum %.3f", path, number, min))
	}
	if max, ok := schema["maximum"].(float64); ok && number > max {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %.3f is greater than maximum %.3f", path, number, max))
	}
	return diagnostics
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}
