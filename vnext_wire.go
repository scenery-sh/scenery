package scenery

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type contractWireType struct {
	name string
	args []contractWireType
}

// MarshalContractValue applies the edition-2027 schema-directed JSON wire
// representation. Generated contract packages use it for record fields so
// exact integers, nested collections, and sets never fall back to Go's
// lossy/default encoding choices.
func MarshalContractValue(value any, typeExpression string) ([]byte, error) {
	typeValue, err := parseContractWireType(typeExpression)
	if err != nil {
		return nil, err
	}
	return marshalContractReflect(reflect.ValueOf(value), typeValue)
}

// UnmarshalContractValue decodes one schema-directed edition-2027 value into
// target. Target must be a non-nil pointer.
func UnmarshalContractValue(data []byte, target any, typeExpression string) error {
	return UnmarshalContractValueWithNamed(data, target, typeExpression, nil)
}

// ContractNamedDecoder lets a generated package provide codecs for named
// tagged unions while the shared runtime continues to recurse through
// optional, nullable, collection, map, and tuple wrappers.
type ContractNamedDecoder func(typeName string, data []byte) (any, error)

func UnmarshalContractValueWithNamed(data []byte, target any, typeExpression string, named ContractNamedDecoder) error {
	typeValue, err := parseContractWireType(typeExpression)
	if err != nil {
		return err
	}
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Pointer || targetValue.IsNil() {
		return fmt.Errorf("contract decode target must be a non-nil pointer")
	}
	return unmarshalContractReflect(data, targetValue.Elem(), typeValue, named)
}

func parseContractWireType(source string) (contractWireType, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return contractWireType{}, fmt.Errorf("empty contract type expression")
	}
	open := strings.IndexByte(source, '(')
	if open < 0 {
		if strings.ContainsAny(source, "),") {
			return contractWireType{}, fmt.Errorf("invalid contract type expression %q", source)
		}
		return contractWireType{name: source}, nil
	}
	if !strings.HasSuffix(source, ")") {
		return contractWireType{}, fmt.Errorf("invalid contract type expression %q", source)
	}
	name := strings.TrimSpace(source[:open])
	if name == "" {
		return contractWireType{}, fmt.Errorf("invalid contract type expression %q", source)
	}
	parts, err := splitContractWireArguments(source[open+1 : len(source)-1])
	if err != nil {
		return contractWireType{}, err
	}
	if name != "tuple" && len(parts) != 1 || name == "tuple" && len(parts) == 0 {
		return contractWireType{}, fmt.Errorf("invalid %s type arity", name)
	}
	result := contractWireType{name: name, args: make([]contractWireType, len(parts))}
	for index, part := range parts {
		result.args[index], err = parseContractWireType(part)
		if err != nil {
			return contractWireType{}, err
		}
	}
	return result, nil
}

func splitContractWireArguments(source string) ([]string, error) {
	depth, start := 0, 0
	var result []string
	for index, character := range source {
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced contract type expression")
			}
		case ',':
			if depth == 0 {
				part := strings.TrimSpace(source[start:index])
				if part == "" {
					return nil, fmt.Errorf("empty contract type argument")
				}
				result = append(result, part)
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced contract type expression")
	}
	part := strings.TrimSpace(source[start:])
	if part == "" {
		return nil, fmt.Errorf("empty contract type argument")
	}
	return append(result, part), nil
}

func marshalContractReflect(value reflect.Value, typeValue contractWireType) ([]byte, error) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return []byte("null"), nil
		}
		value = value.Elem()
	}
	switch typeValue.name {
	case "std.type.unit":
		if !value.IsValid() || value.Kind() != reflect.Struct || value.NumField() != 0 {
			return nil, fmt.Errorf("unit value must be an empty struct")
		}
		return []byte("{}"), nil
	case "optional":
		set := value.FieldByName("Set")
		if !set.IsValid() || !set.Bool() {
			return nil, fmt.Errorf("cannot encode an absent optional value directly")
		}
		return marshalContractReflect(value.FieldByName("Value"), typeValue.args[0])
	case "nullable":
		null := value.FieldByName("Null")
		if !null.IsValid() {
			return nil, fmt.Errorf("invalid nullable representation")
		}
		if null.Bool() {
			return []byte("null"), nil
		}
		return marshalContractReflect(value.FieldByName("Value"), typeValue.args[0])
	case "list", "set":
		if !value.IsValid() || value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
			return nil, fmt.Errorf("%s value must be a slice or array", typeValue.name)
		}
		items := make([][]byte, value.Len())
		for index := 0; index < value.Len(); index++ {
			encoded, err := marshalContractReflect(value.Index(index), typeValue.args[0])
			if err != nil {
				return nil, fmt.Errorf("encode item %d: %w", index, err)
			}
			items[index] = encoded
		}
		if typeValue.name == "set" {
			sort.Slice(items, func(i, j int) bool { return bytes.Compare(items[i], items[j]) < 0 })
			for index := 1; index < len(items); index++ {
				if bytes.Equal(items[index-1], items[index]) {
					return nil, fmt.Errorf("duplicate set element")
				}
			}
		}
		return joinContractJSONArray(items), nil
	case "map":
		if !value.IsValid() || value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map value must use string keys")
		}
		members := make(map[string][]byte, value.Len())
		for _, key := range value.MapKeys() {
			if !utf8.ValidString(key.String()) {
				return nil, fmt.Errorf("map contains invalid UTF-8 key")
			}
			encoded, err := marshalContractReflect(value.MapIndex(key), typeValue.args[0])
			if err != nil {
				return nil, fmt.Errorf("encode map member %q: %w", key.String(), err)
			}
			members[key.String()] = encoded
		}
		return joinContractJSONObject(members)
	case "tuple":
		if !value.IsValid() || value.Kind() != reflect.Struct {
			return nil, fmt.Errorf("tuple value must be a positional struct")
		}
		items := make([][]byte, len(typeValue.args))
		for index, itemType := range typeValue.args {
			field := value.FieldByName(fmt.Sprintf("Item%d", index))
			if !field.IsValid() {
				return nil, fmt.Errorf("tuple is missing Item%d", index)
			}
			encoded, err := marshalContractReflect(field, itemType)
			if err != nil {
				return nil, fmt.Errorf("encode tuple item %d: %w", index, err)
			}
			items[index] = encoded
		}
		return joinContractJSONArray(items), nil
	case "json":
		if value.IsValid() && value.Type() == reflect.TypeFor[json.RawMessage]() {
			return canonicalizeExactJSON(append([]byte(nil), value.Bytes()...))
		}
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return nil, err
		}
		return canonicalizeExactJSON(encoded)
	case "int64":
		if value.Kind() != reflect.Int64 {
			return nil, fmt.Errorf("int64 value has type %s", value.Type())
		}
		return json.Marshal(strconv.FormatInt(value.Int(), 10))
	case "uint64":
		if value.Kind() != reflect.Uint64 {
			return nil, fmt.Errorf("uint64 value has type %s", value.Type())
		}
		return json.Marshal(strconv.FormatUint(value.Uint(), 10))
	case "float32", "float64":
		bits := 64
		if typeValue.name == "float32" {
			bits = 32
		}
		floating := value.Convert(reflect.TypeFor[float64]()).Float()
		if math.IsNaN(floating) || math.IsInf(floating, 0) || floating == 0 && math.Signbit(floating) {
			return nil, fmt.Errorf("%s must be finite and cannot be negative zero", typeValue.name)
		}
		return []byte(strconv.FormatFloat(floating, 'g', -1, bits)), nil
	case "string":
		if value.Kind() != reflect.String || !utf8.ValidString(value.String()) {
			return nil, fmt.Errorf("string value is not valid UTF-8")
		}
		return json.Marshal(value.String())
	case "bool", "int32", "uint32", "bytes":
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return nil, err
		}
		if typeValue.name == "bytes" {
			var text string
			if err := json.Unmarshal(encoded, &text); err != nil {
				return nil, err
			}
			decoded, err := base64.StdEncoding.DecodeString(text)
			if err != nil || base64.StdEncoding.EncodeToString(decoded) != text {
				return nil, fmt.Errorf("bytes did not use padded RFC 4648 base64")
			}
		}
		return encoded, nil
	default:
		if !value.IsValid() {
			return []byte("null"), nil
		}
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return nil, err
		}
		return canonicalizeExactJSON(encoded)
	}
}

func unmarshalContractReflect(data []byte, target reflect.Value, typeValue contractWireType, named ContractNamedDecoder) error {
	if !target.CanAddr() {
		return fmt.Errorf("contract decode target is not addressable")
	}
	switch typeValue.name {
	case "std.type.unit":
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
			return fmt.Errorf("unit value must be an empty object")
		}
		var object map[string]json.RawMessage
		if err := decodeStrictContractJSON(data, &object); err != nil || len(object) != 0 {
			return fmt.Errorf("unit value must be an empty object")
		}
		target.Set(reflect.Zero(target.Type()))
		return nil
	case "optional":
		target.FieldByName("Set").SetBool(true)
		return unmarshalContractReflect(data, target.FieldByName("Value"), typeValue.args[0], named)
	case "nullable":
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			target.FieldByName("Null").SetBool(true)
			target.FieldByName("Value").Set(reflect.Zero(target.FieldByName("Value").Type()))
			return nil
		}
		target.FieldByName("Null").SetBool(false)
		return unmarshalContractReflect(data, target.FieldByName("Value"), typeValue.args[0], named)
	case "list", "set":
		var items []json.RawMessage
		if err := decodeStrictContractJSON(data, &items); err != nil {
			return fmt.Errorf("decode %s: %w", typeValue.name, err)
		}
		decoded := reflect.MakeSlice(target.Type(), len(items), len(items))
		seen := map[string]bool{}
		for index, item := range items {
			if err := unmarshalContractReflect(item, decoded.Index(index), typeValue.args[0], named); err != nil {
				return fmt.Errorf("decode item %d: %w", index, err)
			}
			if typeValue.name == "set" {
				canonical, err := marshalContractReflect(decoded.Index(index), typeValue.args[0])
				if err != nil {
					return err
				}
				key := string(canonical)
				if seen[key] {
					return fmt.Errorf("duplicate set element")
				}
				seen[key] = true
			}
		}
		target.Set(decoded)
		return nil
	case "map":
		var members map[string]json.RawMessage
		if err := decodeStrictContractJSON(data, &members); err != nil {
			return fmt.Errorf("decode map: %w", err)
		}
		decoded := reflect.MakeMapWithSize(target.Type(), len(members))
		for key, item := range members {
			value := reflect.New(target.Type().Elem()).Elem()
			if err := unmarshalContractReflect(item, value, typeValue.args[0], named); err != nil {
				return fmt.Errorf("decode map member %q: %w", key, err)
			}
			decoded.SetMapIndex(reflect.ValueOf(key).Convert(target.Type().Key()), value)
		}
		target.Set(decoded)
		return nil
	case "tuple":
		var items []json.RawMessage
		if err := decodeStrictContractJSON(data, &items); err != nil {
			return fmt.Errorf("decode tuple: %w", err)
		}
		if len(items) != len(typeValue.args) {
			return fmt.Errorf("tuple requires %d items; received %d", len(typeValue.args), len(items))
		}
		for index, item := range items {
			field := target.FieldByName(fmt.Sprintf("Item%d", index))
			if !field.IsValid() {
				return fmt.Errorf("tuple target is missing Item%d", index)
			}
			if err := unmarshalContractReflect(item, field, typeValue.args[index], named); err != nil {
				return fmt.Errorf("decode tuple item %d: %w", index, err)
			}
		}
		return nil
	case "json":
		canonical, err := canonicalizeExactJSON(data)
		if err != nil {
			return err
		}
		if target.Type() != reflect.TypeFor[json.RawMessage]() {
			return json.Unmarshal(canonical, target.Addr().Interface())
		}
		target.SetBytes(append([]byte(nil), canonical...))
		return nil
	case "int64":
		var text string
		if err := decodeStrictContractJSON(data, &text); err != nil {
			return fmt.Errorf("int64 must be a canonical JSON string: %w", err)
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil || strconv.FormatInt(parsed, 10) != text {
			return fmt.Errorf("invalid canonical int64 %q", text)
		}
		target.SetInt(parsed)
		return nil
	case "uint64":
		var text string
		if err := decodeStrictContractJSON(data, &text); err != nil {
			return fmt.Errorf("uint64 must be a canonical JSON string: %w", err)
		}
		parsed, err := strconv.ParseUint(text, 10, 64)
		if err != nil || strconv.FormatUint(parsed, 10) != text {
			return fmt.Errorf("invalid canonical uint64 %q", text)
		}
		target.SetUint(parsed)
		return nil
	case "float32", "float64":
		bits := 64
		if typeValue.name == "float32" {
			bits = 32
		}
		text := strings.TrimSpace(string(data))
		parsed, err := strconv.ParseFloat(text, bits)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed == 0 && math.Signbit(parsed) || strconv.FormatFloat(parsed, 'g', -1, bits) != text {
			return fmt.Errorf("invalid canonical %s", typeValue.name)
		}
		target.SetFloat(parsed)
		return nil
	default:
		if (strings.HasPrefix(typeValue.name, "union.") || strings.Contains(typeValue.name, "/union/")) && named != nil {
			decoded, err := named(typeValue.name, append([]byte(nil), data...))
			if err != nil {
				return err
			}
			decodedValue := reflect.ValueOf(decoded)
			if !decodedValue.IsValid() || !decodedValue.Type().AssignableTo(target.Type()) {
				return fmt.Errorf("named decoder returned %T for %s", decoded, target.Type())
			}
			target.Set(decodedValue)
			return nil
		}
		canonical, err := canonicalizeExactJSON(data)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(canonical, target.Addr().Interface()); err != nil {
			return err
		}
		if typeValue.name == "bytes" {
			var text string
			if err := json.Unmarshal(canonical, &text); err != nil {
				return err
			}
			decoded, err := base64.StdEncoding.DecodeString(text)
			if err != nil || base64.StdEncoding.EncodeToString(decoded) != text {
				return fmt.Errorf("bytes must use padded RFC 4648 base64")
			}
		}
		return nil
	}
}

func joinContractJSONArray(items [][]byte) []byte {
	var output bytes.Buffer
	output.WriteByte('[')
	for index, item := range items {
		if index > 0 {
			output.WriteByte(',')
		}
		output.Write(item)
	}
	output.WriteByte(']')
	return output.Bytes()
}
