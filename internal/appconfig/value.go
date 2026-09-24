package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"scenery.sh/internal/contract"
)

// MaxValueBytes bounds one configured non-secret value.
const MaxValueBytes = 64 << 10

// MaxSecretBytes bounds one secret value.
const MaxSecretBytes = 64 << 10

var scalarGoTypes = map[string]reflect.Type{
	"string":        reflect.TypeFor[string](),
	"bool":          reflect.TypeFor[bool](),
	"int":           reflect.TypeFor[contract.Int](),
	"int32":         reflect.TypeFor[int32](),
	"int64":         reflect.TypeFor[int64](),
	"uint32":        reflect.TypeFor[uint32](),
	"uint64":        reflect.TypeFor[uint64](),
	"decimal":       reflect.TypeFor[contract.Decimal](),
	"float32":       reflect.TypeFor[float32](),
	"float64":       reflect.TypeFor[float64](),
	"uuid":          reflect.TypeFor[contract.UUID](),
	"date":          reflect.TypeFor[contract.Date](),
	"datetime":      reflect.TypeFor[contract.DateTime](),
	"duration":      reflect.TypeFor[contract.Duration](),
	"size":          reflect.TypeFor[contract.Size](),
	"url":           reflect.TypeFor[contract.URL](),
	"relative_path": reflect.TypeFor[contract.RelativePath](),
	"host_path":     reflect.TypeFor[contract.HostPath](),
}

// numberWire lists scalar types whose contract wire form is a JSON number.
var numberWire = map[string]bool{"int32": true, "uint32": true, "float32": true, "float64": true}

// valueType returns the type a configured value must satisfy: an optional
// input's value is its inner type, because absence is expressed by not
// configuring (or by an explicit null), never by the wire value itself.
func valueType(input Input) string {
	typeExpression := strings.TrimSpace(input.Type)
	if strings.HasPrefix(typeExpression, "optional(") && strings.HasSuffix(typeExpression, ")") {
		return strings.TrimSpace(typeExpression[len("optional(") : len(typeExpression)-1])
	}
	return typeExpression
}

func goTypeFor(typeExpression string) (reflect.Type, error) {
	if scalar, ok := scalarGoTypes[typeExpression]; ok {
		return scalar, nil
	}
	for _, wrapper := range []string{"list(", "set("} {
		if strings.HasPrefix(typeExpression, wrapper) && strings.HasSuffix(typeExpression, ")") {
			inner, err := goTypeFor(strings.TrimSpace(typeExpression[len(wrapper) : len(typeExpression)-1]))
			if err != nil {
				return nil, err
			}
			return reflect.SliceOf(inner), nil
		}
	}
	return nil, fmt.Errorf("type %s is not configurable", typeExpression)
}

// ParseText converts command-line text into the input's canonical wire JSON.
// Strings are literal; numbers and booleans follow their declared types;
// collections take JSON text.
func ParseText(input Input, text string) (json.RawMessage, error) {
	if input.Sensitive {
		return nil, fmt.Errorf("%s is a secret; its value is read from a prompt or --stdin", input.Key)
	}
	typeExpression := valueType(input)
	var wire []byte
	switch {
	case typeExpression == "bool":
		switch text {
		case "true", "false":
			wire = []byte(text)
		default:
			return nil, fmt.Errorf("%s requires true or false", input.Key)
		}
	case numberWire[typeExpression]:
		if _, err := strconv.ParseFloat(text, 64); err != nil || strings.TrimSpace(text) != text {
			return nil, fmt.Errorf("%s requires a %s number", input.Key, typeExpression)
		}
		wire = []byte(text)
	case strings.HasPrefix(typeExpression, "list(") || strings.HasPrefix(typeExpression, "set("):
		wire = []byte(text)
	default:
		encoded, err := json.Marshal(text)
		if err != nil {
			return nil, err
		}
		wire = encoded
	}
	return CanonicalValue(input, wire)
}

// CanonicalValue validates wire JSON against the input's declared type and
// constraints and returns its canonical encoding. JSON null is accepted only
// for optional inputs and means configured absence.
func CanonicalValue(input Input, wire json.RawMessage) (json.RawMessage, error) {
	if input.Sensitive {
		return nil, fmt.Errorf("%s is a secret and has no plain configured value", input.Key)
	}
	if len(wire) > MaxValueBytes {
		return nil, fmt.Errorf("%s value exceeds %d bytes", input.Key, MaxValueBytes)
	}
	if bytes.Equal(bytes.TrimSpace(wire), []byte("null")) {
		if !input.Optional {
			return nil, fmt.Errorf("%s is required and cannot be null", input.Key)
		}
		return json.RawMessage("null"), nil
	}
	typeExpression := valueType(input)
	goType, err := goTypeFor(typeExpression)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", input.Key, err)
	}
	target := reflect.New(goType)
	if err := contract.UnmarshalContractValue(wire, target.Interface(), typeExpression); err != nil {
		return nil, fmt.Errorf("%s requires %s: %w", input.Key, typeExpression, err)
	}
	constraints, err := contractConstraints(input.Constraints)
	if err != nil {
		return nil, fmt.Errorf("%s constraints: %w", input.Key, err)
	}
	if err := contract.ValidateContractValue(target.Elem().Interface(), typeExpression, constraints); err != nil {
		return nil, fmt.Errorf("%s: %w", input.Key, err)
	}
	canonical, err := contract.MarshalContractValue(target.Elem().Interface(), typeExpression)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", input.Key, err)
	}
	return canonical, nil
}

func contractConstraints(values map[string]any) (contract.ContractConstraints, error) {
	var constraints contract.ContractConstraints
	number := func(value any) (*string, error) {
		switch typed := value.(type) {
		case string:
			if _, ok := new(big.Rat).SetString(typed); !ok {
				return nil, fmt.Errorf("invalid numeric constraint %q", typed)
			}
			return &typed, nil
		case float64:
			text := strconv.FormatFloat(typed, 'f', -1, 64)
			return &text, nil
		case json.Number:
			text := typed.String()
			return &text, nil
		case int:
			text := strconv.Itoa(typed)
			return &text, nil
		default:
			return nil, fmt.Errorf("invalid numeric constraint %v", value)
		}
	}
	integer := func(value any) (*int64, error) {
		text, err := number(value)
		if err != nil {
			return nil, err
		}
		parsed, err := strconv.ParseInt(*text, 10, 64)
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	}
	var err error
	for name, value := range values {
		switch name {
		case "minimum":
			constraints.Minimum, err = number(value)
		case "maximum":
			constraints.Maximum, err = number(value)
		case "min_length":
			constraints.MinLength, err = integer(value)
		case "max_length":
			constraints.MaxLength, err = integer(value)
		case "min_items":
			constraints.MinItems, err = integer(value)
		case "max_items":
			constraints.MaxItems, err = integer(value)
		case "pattern":
			constraints.Pattern = stringValue(value)
		case "format":
			constraints.Format = stringValue(value)
		case "unique_items":
			constraints.UniqueItems = value == true
		}
		if err != nil {
			return contract.ContractConstraints{}, err
		}
	}
	return constraints, nil
}
