package clientprojection

import (
	"strings"
)

func WireName(field map[string]any, fallback string) string {
	for _, key := range []string{"wire_name", "wire_value", "wire_tag"} {
		if value, ok := field[key].(string); ok {
			return value
		}
	}
	return fallback
}
func GoName(value string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' }) {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}
