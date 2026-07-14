package redact

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

const Placeholder = "[redacted]"

var sensitiveAssignmentRE = regexp.MustCompile(`(?i)\b(authorization|token|access[_-]?token|refresh[_-]?token|password|secret|api[_-]?key|database[_-]?url|jwt)\b(\s*[:=]\s*)([^,\s;]+)`)
var bearerTokenRE = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`)

func Value(value any) any {
	return redactor{seen: make(map[visit]bool)}.value(reflect.ValueOf(value), "")
}

func String(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if redacted, ok := URL(value); ok {
		return redacted
	}
	value = bearerTokenRE.ReplaceAllString(value, "Bearer "+Placeholder)
	return sensitiveAssignmentRE.ReplaceAllStringFunc(value, func(match string) string {
		parts := sensitiveAssignmentRE.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		return parts[1] + parts[2] + Placeholder
	})
}

func URL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if username != "" {
			parsed.User = url.UserPassword(username, Placeholder)
		} else {
			parsed.User = url.User(Placeholder)
		}
	}
	query := parsed.Query()
	if len(query) > 0 {
		for key := range query {
			if SensitiveKey(key) {
				query.Set(key, Placeholder)
			}
		}
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), true
}

func Headers(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if SensitiveKey(key) {
			out[key] = Placeholder
			continue
		}
		out[key] = strings.Join(values, ", ")
	}
	return out
}

func Metadata(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	r := redactor{seen: make(map[visit]bool)}
	for key, value := range meta {
		out[key] = r.any(value, key)
	}
	return out
}

func SensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	switch normalized {
	case "authorization", "cookie", "setcookie", "token", "accesstoken", "refreshtoken", "password", "secret", "apikey", "databaseurl", "jwt", "jwttoken":
		return true
	default:
		return false
	}
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

type redactor struct {
	seen map[visit]bool
}

func (r redactor) any(value any, key string) any {
	if SensitiveKey(key) {
		return Placeholder
	}
	return r.value(reflect.ValueOf(value), key)
}

func (r redactor) value(value reflect.Value, key string) any {
	if !value.IsValid() {
		return nil
	}
	if SensitiveKey(key) {
		return Placeholder
	}
	// Errors almost never expose exported fields, so the struct walk below
	// would erase them to an empty map (logs then read `error=map[]`).
	// Preserve the message and scrub it like any other string.
	if value.CanInterface() {
		if err, ok := value.Interface().(error); ok && err != nil {
			return String(err.Error())
		}
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return r.value(value.Elem(), key)
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		ref := visit{typ: value.Type(), ptr: value.Pointer()}
		if value.Pointer() != 0 {
			if r.seen[ref] {
				return Placeholder
			}
			r.seen[ref] = true
		}
		return r.value(value.Elem(), key)
	case reflect.Struct:
		if value.Type().PkgPath() == "time" && value.Type().Name() == "Time" {
			return value.Interface()
		}
		out := make(map[string]any)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			fieldName := jsonFieldName(field)
			if isSensitiveField(field) || SensitiveKey(fieldName) {
				out[fieldName] = Placeholder
				continue
			}
			out[fieldName] = r.value(value.Field(i), fieldName)
		}
		return out
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		out := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			mapKey := stringifyMapKey(iter.Key())
			if SensitiveKey(mapKey) {
				out[mapKey] = Placeholder
				continue
			}
			out[mapKey] = r.value(iter.Value(), mapKey)
		}
		return out
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil
		}
		out := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			out[i] = r.value(value.Index(i), key)
		}
		return out
	default:
		if value.CanInterface() {
			return value.Interface()
		}
		return nil
	}
}

func isSensitiveField(field reflect.StructField) bool {
	for part := range strings.SplitSeq(field.Tag.Get("scenery"), ",") {
		if strings.TrimSpace(part) == "sensitive" {
			return true
		}
	}
	return false
}

func jsonFieldName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "" {
		return field.Name
	}
	if name == "-" {
		return field.Name
	}
	return name
}

func stringifyMapKey(key reflect.Value) string {
	if !key.IsValid() {
		return ""
	}
	if key.Kind() == reflect.String {
		return key.String()
	}
	if key.CanInterface() {
		return fmt.Sprint(key.Interface())
	}
	return ""
}

func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("-", "", "_", "", " ", "", ".", "")
	return replacer.Replace(key)
}
