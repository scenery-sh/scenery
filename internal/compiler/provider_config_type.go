package compiler

import (
	"strconv"
	"strings"
)

func deploymentValueMatchesType(value any, typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	if inner, ok := deploymentWrappedType(typeName, "optional"); ok {
		return value != nil && deploymentValueMatchesType(value, inner)
	}
	if inner, ok := deploymentWrappedType(typeName, "nullable"); ok {
		return value == nil || deploymentValueMatchesType(value, inner)
	}
	switch typeName {
	case "string", "relative_path", "host_path", "url", "uuid", "date", "datetime", "duration":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "int", "int32", "int64":
		_, err := strconv.ParseInt(stringValue(value), 10, 64)
		return err == nil
	case "uint32", "uint64", "size":
		_, err := strconv.ParseUint(stringValue(value), 10, 64)
		return err == nil
	case "decimal", "float32", "float64":
		_, err := strconv.ParseFloat(stringValue(value), 64)
		return err == nil
	default:
		return value != nil
	}
}

func deploymentWrappedType(typeName, wrapper string) (string, bool) {
	prefix := wrapper + "("
	if !strings.HasPrefix(typeName, prefix) || !strings.HasSuffix(typeName, ")") {
		return "", false
	}
	inner := strings.TrimSpace(typeName[len(prefix) : len(typeName)-1])
	return inner, inner != ""
}
