package clientprojection

import (
	"strings"
)

func TSType(value any) string {
	ref := RefString(value)
	if ref != "" {
		for _, segment := range []string{"/record/", "/enum/", "/union/"} {
			if index := strings.LastIndex(ref, segment); index >= 0 {
				return GoName(ref[index+len(segment):])
			}
		}
		parts := strings.Split(ref, ".")
		if len(parts) >= 2 && (parts[0] == "record" || parts[0] == "enum" || parts[0] == "union") {
			return GoName(parts[1])
		}
		if ref == "std.type.problem" {
			return "Problem"
		}
		if ref == "std.type.unit" {
			return "Unit"
		}
		if ref == "std.type.execution_receipt" {
			return "EnqueueReceipt"
		}
		switch ref {
		case "bool":
			return "boolean"
		case "int", "int64", "uint64", "size":
			return "bigint"
		case "int32", "uint32", "float32", "float64":
			return "number"
		case "decimal":
			return "DecimalString"
		case "string":
			return "string"
		case "bytes":
			return "Uint8Array"
		case "uuid":
			return "UUIDString"
		case "date":
			return "DateString"
		case "datetime":
			return "DateTimeString"
		case "duration":
			return "DurationString"
		case "url":
			return "URLString"
		case "relative_path":
			return "RelativePathString"
		case "json":
			return "JsonValue"
		}
	}
	if m, ok := value.(map[string]any); ok {
		if raw, ok := m["$expression"].(string); ok {
			return TSTypeExpression(raw)
		}
	}
	return "unknown"
}
func TSTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, wrapper := range []struct{ prefix, before, after string }{{"optional(", "", ""}, {"nullable(", "", " | null"}, {"list(", "readonly ", "[]"}, {"set(", "readonly ", "[]"}, {"map(", "Readonly<Record<string, ", ">>"}} {
		if strings.HasPrefix(raw, wrapper.prefix) && strings.HasSuffix(raw, ")") {
			inner := TSTypeExpression(strings.TrimSuffix(strings.TrimPrefix(raw, wrapper.prefix), ")"))
			return wrapper.before + inner + wrapper.after
		}
	}
	return TSType(map[string]any{"$ref": raw})
}

func TSName(value string) string {
	name := GoName(value)
	if name == "" {
		return ""
	}
	return strings.ToLower(name[:1]) + name[1:]
}
func LastRef(value string) string {
	value = strings.TrimSpace(value)
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	parts := strings.Split(value, ".")
	return parts[len(parts)-1]
}
