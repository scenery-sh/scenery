package runtime

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

func encodeResponseWithStatus(w http.ResponseWriter, resp any, explicitStatus int) error {
	status := http.StatusOK
	if explicitStatus != 0 {
		status = explicitStatus
	}
	if resp == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, err := w.Write([]byte("null"))
		return err
	}

	value := reflect.ValueOf(resp)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, err := w.Write([]byte("null"))
		return err
	}

	if isStructLike(value.Type()) {
		body, bodyStatus, err := splitResponse(resp, w.Header())
		if err != nil {
			return err
		}
		if explicitStatus == 0 && bodyStatus != 0 {
			status = bodyStatus
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		return json.NewEncoder(w).Encode(body)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(resp)
}

func splitResponse(resp any, headers http.Header) (any, int, error) {
	value := reflect.ValueOf(resp)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	typ := value.Type()
	if !hasResponseShapeTags(typ) {
		return resp, 0, nil
	}
	body := make(map[string]any)
	status := 0

	if err := appendResponseFields(body, &status, value, headers); err != nil {
		return nil, 0, err
	}
	return body, status, nil
}

func appendResponseFields(body map[string]any, status *int, value reflect.Value, headers http.Header) error {
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		fieldValue := value.Field(i)
		if shouldFlattenJSONField(field, fieldValue) {
			if fieldValue.Kind() == reflect.Pointer {
				if fieldValue.IsNil() {
					continue
				}
				fieldValue = fieldValue.Elem()
			}
			if err := appendResponseFields(body, status, fieldValue, headers); err != nil {
				return err
			}
			continue
		}
		if headerName := field.Tag.Get("header"); headerName != "" {
			headers.Set(headerName, formatHeaderValue(fieldValue))
			continue
		}
		if hasSceneryTag(field, "httpstatus") {
			if fieldValue.CanInt() {
				*status = int(fieldValue.Int())
			}
			continue
		}
		name, opts := parseJSONTag(field)
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if opts["omitempty"] && isEmptyJSONValue(fieldValue) {
			continue
		}
		body[name] = fieldValue.Interface()
	}
	return nil
}

func hasResponseShapeTags(typ reflect.Type) bool {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return false
	}
	for field := range typ.Fields() {
		if !field.IsExported() {
			continue
		}
		if field.Tag.Get("header") != "" || hasSceneryTag(field, "httpstatus") {
			return true
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if field.Anonymous && field.Tag.Get("json") == "" && fieldType.Kind() == reflect.Struct && hasResponseShapeTags(fieldType) {
			return true
		}
	}
	return false
}

func shouldFlattenJSONField(field reflect.StructField, value reflect.Value) bool {
	if !field.Anonymous || field.Tag.Get("json") != "" {
		return false
	}
	typ := value.Type()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.Kind() == reflect.Struct
}

func parseJSONTag(field reflect.StructField) (string, map[string]bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return "", nil
	}
	parts := strings.Split(tag, ",")
	opts := make(map[string]bool, len(parts)-1)
	for _, opt := range parts[1:] {
		if opt != "" {
			opts[opt] = true
		}
	}
	return parts[0], opts
}

func isEmptyJSONValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool:
		return !value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return value.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	default:
		return false
	}
}

func hasSceneryTag(field reflect.StructField, want string) bool {
	tag := field.Tag.Get("scenery")
	if tag == "" {
		return false
	}
	for part := range strings.SplitSeq(tag, ",") {
		if strings.TrimSpace(part) == want {
			return true
		}
	}
	return false
}

func formatHeaderValue(value reflect.Value) string {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		return formatHeaderValue(value.Elem())
	}
	if value.CanInterface() {
		if marshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](value); ok {
			data, err := marshaler.MarshalText()
			if err == nil {
				return string(data)
			}
		}
	}
	return fmt.Sprint(value.Interface())
}
