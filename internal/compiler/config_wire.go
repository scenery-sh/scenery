package compiler

import (
	"encoding/json"
	"fmt"
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
	if scalar, ok := value.(map[string]any); ok && stringValue(scalar["$scalar"]) != "" {
		kind := stringValue(scalar["$scalar"])
		switch kind {
		case "int":
			text := stringValue(scalar["value"])
			switch typeExpression {
			case "int32", "uint32", "float32", "float64":
				return []byte(text), nil
			default:
				return json.Marshal(text)
			}
		case "decimal":
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
	case "int", "int64", "uint64", "decimal", "size":
		return json.Marshal(fmt.Sprint(value))
	case "int32", "uint32", "float32", "float64":
		text := fmt.Sprint(value)
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return nil, err
		}
		return []byte(text), nil
	default:
		return json.Marshal(value)
	}
}
