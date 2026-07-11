package vnext

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
)

var fieldConstraintKeys = map[string]bool{
	"name": true, "type": true, "wire_name": true, "default": true,
	"minimum": true, "maximum": true, "min_length": true, "max_length": true,
	"pattern": true, "format": true, "min_items": true, "max_items": true,
	"unique_items": true, "sensitive": true, "immutable": true, "deprecated": true,
}

func validateConstraints(resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, resource := range resources {
		if resource.Kind != "scenery.record/v1" {
			continue
		}
		unknownFields, _ := resource.Spec["unknown_fields"].(string)
		if unknownFields != "" && unknownFields != "reject" && unknownFields != "preserve" {
			diagnostics = append(diagnostics, constraintDiagnostic("SCN1220", "unknown_fields must be reject or preserve", resource, ""))
		}
		seen := map[string]bool{}
		for _, field := range namedChildren(resource.Spec, "field") {
			name := stringValue(field["name"])
			if name == "" || seen[name] {
				diagnostics = append(diagnostics, constraintDiagnostic("SCN1221", "record field names must be non-empty and unique", resource, name))
			}
			seen[name] = true
			for key := range field {
				if !fieldConstraintKeys[key] {
					diagnostics = append(diagnostics, constraintDiagnostic("SCN1222", "unknown record field attribute "+key, resource, name))
				}
			}
			diagnostics = append(diagnostics, validateFieldConstraints(resource, field)...)
		}
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

func validateFieldConstraints(resource Resource, field map[string]any) []Diagnostic {
	name := stringValue(field["name"])
	expression := unwrapConstraintType(typeExpression(field["type"]))
	var diagnostics []Diagnostic
	add := func(code, message string) {
		diagnostics = append(diagnostics, constraintDiagnostic(code, message, resource, name))
	}
	numeric := isNumericType(expression)
	collection := strings.HasPrefix(expression, "list(") || strings.HasPrefix(expression, "set(") || strings.HasPrefix(expression, "map(") || strings.HasPrefix(expression, "tuple(")
	stringLike := expression == "string"
	for _, key := range []string{"minimum", "maximum"} {
		if value, exists := field[key]; exists {
			if !numeric || exactConstraintNumber(value) == nil {
				add("SCN1223", key+" requires an exact numeric field and value")
			}
		}
	}
	if minimum, maximum := exactConstraintNumber(field["minimum"]), exactConstraintNumber(field["maximum"]); minimum != nil && maximum != nil && minimum.Cmp(maximum) > 0 {
		add("SCN1224", "minimum cannot exceed maximum")
	}
	for _, pair := range [][2]string{{"min_length", "max_length"}, {"min_items", "max_items"}} {
		minimum, minimumOK := nonNegativeInteger(field[pair[0]])
		maximum, maximumOK := nonNegativeInteger(field[pair[1]])
		if field[pair[0]] != nil && (!minimumOK || (!(stringLike || collection) && pair[0] == "min_length") || (!collection && pair[0] == "min_items")) {
			add("SCN1225", pair[0]+" is not applicable or is not a non-negative integer")
		}
		if field[pair[1]] != nil && (!maximumOK || (!(stringLike || collection) && pair[1] == "max_length") || (!collection && pair[1] == "max_items")) {
			add("SCN1225", pair[1]+" is not applicable or is not a non-negative integer")
		}
		if minimumOK && maximumOK && minimum > maximum {
			add("SCN1226", pair[0]+" cannot exceed "+pair[1])
		}
	}
	if pattern, exists := field["pattern"]; exists {
		text, ok := pattern.(string)
		if !stringLike || !ok {
			add("SCN1227", "pattern requires a string field and literal pattern")
		} else if _, err := regexp.Compile(text); err != nil {
			add("SCN1227", "pattern is not valid RE2 syntax: "+err.Error())
		}
	}
	if format, exists := field["format"]; exists {
		text, ok := format.(string)
		known := map[string]bool{"uuid": true, "date": true, "datetime": true, "duration": true, "url": true, "relative_path": true}
		if !stringLike || !ok || !known[text] {
			add("SCN1228", "format requires a supported standardized string format")
		}
	}
	if unique, exists := field["unique_items"]; exists {
		_, ok := unique.(bool)
		if !ok || (!strings.HasPrefix(expression, "list(") && !strings.HasPrefix(expression, "set(")) {
			add("SCN1229", "unique_items = true requires a list or set field")
		}
	}
	return diagnostics
}

func unwrapConstraintType(expression string) string {
	expression = strings.TrimSpace(expression)
	for {
		changed := false
		for _, wrapper := range []string{"optional", "nullable"} {
			prefix := wrapper + "("
			if strings.HasPrefix(expression, prefix) && strings.HasSuffix(expression, ")") {
				expression = strings.TrimSpace(expression[len(prefix) : len(expression)-1])
				changed = true
			}
		}
		if !changed {
			return expression
		}
	}
}

func isNumericType(expression string) bool {
	switch expression {
	case "int", "int32", "int64", "uint32", "uint64", "decimal", "float32", "float64", "size":
		return true
	default:
		return false
	}
}

func exactConstraintNumber(value any) *big.Rat {
	if value == nil {
		return nil
	}
	text := stringValue(value)
	if text == "" {
		text = fmt.Sprint(value)
	}
	result, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil
	}
	return result
}

func nonNegativeInteger(value any) (int64, bool) {
	if value == nil {
		return 0, false
	}
	rational := exactConstraintNumber(value)
	if rational == nil || !rational.IsInt() || rational.Sign() < 0 || !rational.Num().IsInt64() {
		return 0, false
	}
	return rational.Num().Int64(), true
}

func constraintDiagnostic(code, message string, resource Resource, field string) Diagnostic {
	path := "/spec/field"
	if field != "" {
		path += "/" + field
	}
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address, Path: path}
}
