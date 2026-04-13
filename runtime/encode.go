package runtime

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

func encodeResponse(w http.ResponseWriter, resp any) error {
	status := http.StatusOK
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
		if bodyStatus != 0 {
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
	body := make(map[string]any)
	status := 0

	for i := 0; i < value.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		fieldValue := value.Field(i)
		if headerName := field.Tag.Get("header"); headerName != "" {
			headers.Set(headerName, formatHeaderValue(fieldValue))
			continue
		}
		if hasPulseTag(field, "httpstatus") {
			if fieldValue.CanInt() {
				status = int(fieldValue.Int())
			}
			continue
		}
		body[jsonName(field)] = fieldValue.Interface()
	}
	return body, status, nil
}

func hasPulseTag(field reflect.StructField, want string) bool {
	tag := field.Tag.Get("pulse")
	if tag == "" {
		return false
	}
	for _, part := range strings.Split(tag, ",") {
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
		if marshaler, ok := value.Interface().(encoding.TextMarshaler); ok {
			data, err := marshaler.MarshalText()
			if err == nil {
				return string(data)
			}
		}
	}
	return fmt.Sprint(value.Interface())
}
