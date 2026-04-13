package runtime

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"pulse.dev/errs"
)

func convertScalar(kind ParamKind, value string) (any, error) {
	switch kind {
	case ParamString:
		return value, nil
	case ParamBool:
		return strconv.ParseBool(value)
	case ParamInt:
		v, err := strconv.ParseInt(value, 10, 0)
		return int(v), err
	case ParamInt8:
		v, err := strconv.ParseInt(value, 10, 8)
		return int8(v), err
	case ParamInt16:
		v, err := strconv.ParseInt(value, 10, 16)
		return int16(v), err
	case ParamInt32:
		v, err := strconv.ParseInt(value, 10, 32)
		return int32(v), err
	case ParamInt64:
		return strconv.ParseInt(value, 10, 64)
	case ParamUint:
		v, err := strconv.ParseUint(value, 10, 0)
		return uint(v), err
	case ParamUint8:
		v, err := strconv.ParseUint(value, 10, 8)
		return uint8(v), err
	case ParamUint16:
		v, err := strconv.ParseUint(value, 10, 16)
		return uint16(v), err
	case ParamUint32:
		v, err := strconv.ParseUint(value, 10, 32)
		return uint32(v), err
	case ParamUint64:
		return strconv.ParseUint(value, 10, 64)
	default:
		return nil, fmt.Errorf("unsupported scalar kind %s", kind)
	}
}

func decodePayload(req *http.Request, typ reflect.Type) (any, error) {
	if typ == nil {
		return nil, nil
	}
	if isStructLike(typ) {
		return decodeTaggedStruct(req, typ, false)
	}

	target := newValueForType(typ)
	if req.Body == nil {
		return target.Interface(), nil
	}
	defer req.Body.Close()
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, errs.Wrap(err, "read request body")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return finalizeValue(target, typ), nil
	}
	if err := json.Unmarshal(data, target.Interface()); err != nil {
		return nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid json body: %v", err).Err()
	}
	return finalizeValue(target, typ), nil
}

func decodeTaggedStruct(req *http.Request, typ reflect.Type, authOnly bool) (any, error) {
	target := newValueForType(typ)
	value := target.Elem()
	if value.Kind() == reflect.Ptr {
		value.Set(reflect.New(value.Type().Elem()))
		value = value.Elem()
	}

	useQueryDefaults := req.Method == http.MethodGet || req.Method == http.MethodHead || req.Method == http.MethodDelete || authOnly
	if !useQueryDefaults && req.Body != nil {
		defer req.Body.Close()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, errs.Wrap(err, "read request body")
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, target.Interface()); err != nil {
				return nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid json body: %v", err).Err()
			}
		}
	}

	query := req.URL.Query()
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}

		switch {
		case field.Tag.Get("header") != "":
			if err := setFieldFromStrings(value.Field(i), req.Header.Values(field.Tag.Get("header")), field.Tag.Get("header")); err != nil {
				return nil, err
			}
		case field.Tag.Get("query") != "":
			if err := setFieldFromStrings(value.Field(i), query[field.Tag.Get("query")], field.Tag.Get("query")); err != nil {
				return nil, err
			}
		case field.Tag.Get("qs") != "":
			if err := setFieldFromStrings(value.Field(i), query[field.Tag.Get("qs")], field.Tag.Get("qs")); err != nil {
				return nil, err
			}
		case field.Tag.Get("cookie") != "":
			if cookie, err := req.Cookie(field.Tag.Get("cookie")); err == nil {
				if err := setFieldFromStrings(value.Field(i), []string{cookie.Value}, field.Tag.Get("cookie")); err != nil {
					return nil, err
				}
			}
		case useQueryDefaults:
			if err := setFieldFromStrings(value.Field(i), query[jsonName(field)], jsonName(field)); err != nil {
				return nil, err
			}
		}
	}

	if err := maybeValidate(target.Interface()); err != nil {
		return nil, err
	}
	return finalizeValue(target, typ), nil
}

func setFieldFromStrings(field reflect.Value, values []string, name string) error {
	if len(values) == 0 {
		return nil
	}
	if field.Kind() == reflect.Slice && len(values) == 1 && strings.Contains(values[0], ",") {
		values = strings.Split(values[0], ",")
	}
	if err := assignStrings(field, values); err != nil {
		return errs.B().Code(errs.InvalidArgument).Msgf("invalid value for %s: %v", name, err).Err()
	}
	return nil
}

func assignStrings(field reflect.Value, values []string) error {
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		return assignStrings(field.Elem(), values)
	}
	if len(values) == 0 {
		return nil
	}

	if field.CanAddr() {
		if unmarshaler, ok := field.Addr().Interface().(encoding.TextUnmarshaler); ok {
			return unmarshaler.UnmarshalText([]byte(values[0]))
		}
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(values[0])
	case reflect.Bool:
		v, err := strconv.ParseBool(values[0])
		if err != nil {
			return err
		}
		field.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type().PkgPath() == "time" && field.Type().Name() == "Time" {
			t, err := time.Parse(time.RFC3339, values[0])
			if err != nil {
				return err
			}
			field.Set(reflect.ValueOf(t))
			return nil
		}
		v, err := strconv.ParseInt(values[0], 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(values[0], 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(values[0], field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(v)
	case reflect.Slice:
		slice := reflect.MakeSlice(field.Type(), len(values), len(values))
		for i, item := range values {
			if err := assignStrings(slice.Index(i), []string{item}); err != nil {
				return err
			}
		}
		field.Set(slice)
	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
	return nil
}

func maybeValidate(value any) error {
	type validator interface {
		Validate() error
	}
	if v, ok := value.(validator); ok {
		return v.Validate()
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		if v, ok := rv.Elem().Interface().(validator); ok {
			return v.Validate()
		}
	}
	return nil
}

func isStructLike(typ reflect.Type) bool {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.Kind() == reflect.Struct
}

func newValueForType(typ reflect.Type) reflect.Value {
	if typ.Kind() == reflect.Pointer {
		return reflect.New(typ.Elem())
	}
	return reflect.New(typ)
}

func finalizeValue(target reflect.Value, original reflect.Type) any {
	if original.Kind() == reflect.Pointer {
		return target.Interface()
	}
	return target.Elem().Interface()
}

func jsonName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return field.Name
	}
	return name
}
