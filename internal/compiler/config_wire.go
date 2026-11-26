package compiler

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	scenery "scenery.sh/internal/contract"
)

// ConfigWireJSON encodes one semantic configuration value as the contract wire
// JSON its Go field decodes. An optional(T) value encodes as T.
func ConfigWireJSON(value any, typeExpression string) ([]byte, error) {
	typeExpression = strings.TrimSpace(typeExpression)
	if strings.HasPrefix(typeExpression, "optional(") && strings.HasSuffix(typeExpression, ")") {
		typeExpression = strings.TrimSpace(typeExpression[len("optional(") : len(typeExpression)-1])
	}
	if typeExpression == "json" {
		plain, err := plainJSONValue(value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(plain)
	}
	if scalar, ok := value.(map[string]any); ok && stringValue(scalar["$scalar"]) != "" {
		kind := stringValue(scalar["$scalar"])
		switch kind {
		case "int", "decimal":
			if fixedWidthNumericType(typeExpression) {
				return numericWireJSON(stringValue(scalar), typeExpression)
			}
			return json.Marshal(stringValue(scalar))
		case "duration":
			duration, err := scenery.ParseDuration(stringValue(scalar["nanoseconds"]) + "ns")
			if err != nil {
				return nil, err
			}
			return json.Marshal(duration.String())
		case "size":
			return json.Marshal(stringValue(scalar["bytes"]))
		case "bytes":
			return nil, fmt.Errorf("bytes config requires an explicit generated wire value")
		default:
			return json.Marshal(stringValue(scalar["value"]))
		}
	}
	if reference := refString(value); reference != "" {
		return nil, fmt.Errorf("config type %s does not accept resource reference %s", typeExpression, reference)
	}
	switch typeExpression {
	case "int", "decimal", "size":
		return json.Marshal(fmt.Sprint(value))
	default:
		if fixedWidthNumericType(typeExpression) {
			return numericWireJSON(fmt.Sprint(value), typeExpression)
		}
		return json.Marshal(value)
	}
}

func fixedWidthNumericType(typeExpression string) bool {
	switch typeExpression {
	case "int32", "uint32", "int64", "uint64", "float32", "float64":
		return true
	}
	return false
}

// numericWireJSON checks an exact numeric literal against a fixed-width
// contract type and returns its canonical wire JSON: floats use Go's shortest
// 'g' form (1e+06, 1.5e-07), 64-bit integers are decimal strings.
func numericWireJSON(text, typeExpression string) ([]byte, error) {
	switch typeExpression {
	case "float32", "float64":
		bits := 64
		if typeExpression == "float32" {
			bits = 32
		}
		value, err := strconv.ParseFloat(text, bits)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, fmt.Errorf("value %s is outside %s", text, typeExpression)
		}
		if value == 0 {
			value = 0 // The wire has no negative zero.
		}
		return []byte(strconv.FormatFloat(value, 'g', -1, bits)), nil
	case "int32", "int64":
		bits := 32
		if typeExpression == "int64" {
			bits = 64
		}
		value, err := strconv.ParseInt(text, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("value %s is not a %s", text, typeExpression)
		}
		if bits == 64 {
			return json.Marshal(strconv.FormatInt(value, 10))
		}
		return []byte(strconv.FormatInt(value, 10)), nil
	default:
		bits := 32
		if typeExpression == "uint64" {
			bits = 64
		}
		value, err := strconv.ParseUint(text, 10, bits)
		if err != nil {
			return nil, fmt.Errorf("value %s is not a %s", text, typeExpression)
		}
		if bits == 64 {
			return json.Marshal(strconv.FormatUint(value, 10))
		}
		return []byte(strconv.FormatUint(value, 10)), nil
	}
}

// plainJSONValue converts a compiled json-typed value to plain JSON. The .scn
// loader keeps numeric literals at any depth as exact {"$scalar": ...} maps;
// they become JSON numbers here, and other scalars their text form.
func plainJSONValue(value any) (any, error) {
	if reference := refString(value); reference != "" {
		return nil, fmt.Errorf("json value cannot contain resource reference %s", reference)
	}
	switch typed := value.(type) {
	case map[string]any:
		if kind := stringValue(typed["$scalar"]); kind != "" {
			if kind == "int" || kind == "decimal" {
				return json.Number(stringValue(typed)), nil
			}
			return stringValue(typed), nil
		}
		object := make(map[string]any, len(typed))
		for key, item := range typed {
			converted, err := plainJSONValue(item)
			if err != nil {
				return nil, err
			}
			object[key] = converted
		}
		return object, nil
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			converted, err := plainJSONValue(item)
			if err != nil {
				return nil, err
			}
			items[index] = converted
		}
		return items, nil
	default:
		return value, nil
	}
}
