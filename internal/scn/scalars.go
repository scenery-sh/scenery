package scn

import (
	"encoding/base64"
	"fmt"
	"strings"

	scenery "scenery.sh"
)

var contextualPrimitiveTypes = map[string]bool{"bytes": true, "uuid": true, "date": true, "datetime": true, "duration": true, "size": true, "url": true, "relative_path": true}

// IsContextualPrimitive reports whether a type is normalized from source text.
func IsContextualPrimitive(typeExpression string) bool {
	return contextualPrimitiveTypes[typeExpression]
}

// ContextualizePrimitive parses a source string for one contextual primitive.
func ContextualizePrimitive(value, typeExpression string) (any, error) {
	typeExpression = strings.TrimSpace(typeExpression)
	scalar := func(kind, normalized string) any {
		return map[string]any{"$scalar": kind, "value": normalized}
	}
	switch typeExpression {
	case "bytes":
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || strings.Contains(value, "=") {
			return nil, fmt.Errorf("invalid bytes literal")
		}
		return scalar("bytes", base64.RawURLEncoding.EncodeToString(decoded)), nil
	case "uuid":
		parsed, err := scenery.ParseUUID(value)
		if err != nil {
			return nil, err
		}
		return scalar("uuid", string(parsed)), nil
	case "date":
		parsed, err := scenery.ParseDate(value)
		if err != nil {
			return nil, err
		}
		return scalar("date", string(parsed)), nil
	case "datetime":
		parsed, err := scenery.ParseDateTime(value)
		if err != nil {
			return nil, err
		}
		return scalar("datetime", parsed.String()), nil
	case "duration":
		parsed, err := scenery.ParseDuration(value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"$scalar": "duration", "nanoseconds": parsed.Nanoseconds().String()}, nil
	case "size":
		parsed, err := scenery.ParseSize(value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"$scalar": "size", "bytes": parsed.Bytes().String()}, nil
	case "url":
		parsed, err := scenery.ParseURL(value)
		if err != nil {
			return nil, err
		}
		return scalar("url", parsed.String()), nil
	case "relative_path":
		parsed, err := scenery.ParseRelativePath(value)
		if err != nil {
			return nil, err
		}
		return scalar("relative_path", string(parsed)), nil
	default:
		return value, nil
	}
}
