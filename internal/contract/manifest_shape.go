package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// ManifestTypes is the serializable named wire shape table consumed by the Host.
type ManifestTypes map[string]ManifestShape

type ManifestShape struct {
	Kind          string
	Fields        []ManifestField
	UnknownFields string
	Open          bool
	Values        []string
	Discriminator string
	Variants      map[string]string
}

type ManifestField struct {
	Name        string
	Type        string
	Default     json.RawMessage `json:"default,omitempty"`
	Constraints ContractConstraints
}

// Normalize validates a JSON value against a type in the manifest and encodes
// it with the same scalar codecs and object ordering as native contracts.
func (types ManifestTypes) Normalize(expression string, raw []byte) ([]byte, error) {
	t, err := parseContractWireType(expression)
	if err != nil {
		return nil, err
	}
	return types.normalize(t, raw, ContractConstraints{}, 0)
}

func (types ManifestTypes) normalize(t contractWireType, raw []byte, constraints ContractConstraints, depth int) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if depth >= 128 || len(raw) == 0 {
		return nil, fmt.Errorf("incomplete or too deeply nested contract value")
	}
	switch t.name {
	case "optional":
		return types.normalize(t.args[0], raw, constraints, depth+1)
	case "nullable":
		if bytes.Equal(raw, []byte("null")) {
			return raw, nil
		}
		return types.normalize(t.args[0], raw, constraints, depth+1)
	case "list", "set", "tuple":
		return types.normalizeArray(t, raw, constraints, depth)
	case "map":
		fields, err := DecodeJSONObject(raw)
		if err != nil {
			return nil, err
		}
		result := make(map[string][]byte, len(fields))
		for name, value := range fields {
			encoded, err := types.normalize(t.args[0], value, ContractConstraints{}, depth+1)
			if err != nil {
				return nil, fmt.Errorf("map member %s: %w", name, err)
			}
			result[name] = encoded
		}
		return joinContractJSONObject(result)
	case "json":
		return canonicalizeExactJSON(raw)
	default:
		if shape, exists := types[t.name]; exists {
			return types.normalizeNamed(shape, raw, constraints, depth)
		}
		return normalizeManifestScalar(t, raw, constraints)
	}
}

func (types ManifestTypes) normalizeArray(t contractWireType, raw []byte, constraints ContractConstraints, depth int) ([]byte, error) {
	var array []json.RawMessage
	if raw[0] != '[' || decodeStrictContractJSON(raw, &array) != nil {
		return nil, fmt.Errorf("expected JSON array for %s", t.name)
	}
	if t.name == "tuple" && len(array) != len(t.args) {
		return nil, fmt.Errorf("tuple has %d fields, want %d", len(array), len(t.args))
	}
	length := int64(len(array))
	if constraints.MinItems != nil && length < *constraints.MinItems || constraints.MaxItems != nil && length > *constraints.MaxItems {
		return nil, fmt.Errorf("collection exceeds declared item count limits")
	}
	result := make([][]byte, 0, len(array))
	seen := make(map[string]bool)
	for i, item := range array {
		elementType := t.args[0]
		if t.name == "tuple" {
			elementType = t.args[i]
		}
		encoded, err := types.normalize(elementType, item, ContractConstraints{}, depth+1)
		if err != nil {
			return nil, fmt.Errorf("array item %d: %w", i, err)
		}
		if t.name == "set" || constraints.UniqueItems {
			key := string(encoded)
			if seen[key] {
				return nil, fmt.Errorf("duplicate collection item")
			}
			seen[key] = true
		}
		result = append(result, encoded)
	}
	if t.name == "set" {
		slices.SortFunc(result, bytes.Compare)
	}
	return joinContractJSONArray(result), nil
}

func (types ManifestTypes) normalizeNamed(shape ManifestShape, raw []byte, constraints ContractConstraints, depth int) ([]byte, error) {
	switch shape.Kind {
	case "record":
		return types.normalizeRecord(shape, raw, depth)
	case "enum":
		var value string
		if err := UnmarshalContractValue(raw, &value, "string"); err != nil {
			return nil, err
		}
		if !shape.Open && !slices.Contains(shape.Values, value) {
			return nil, fmt.Errorf("unknown enum value")
		}
		return normalizeManifestScalar(contractWireType{name: "string"}, raw, constraints)
	case "union":
		return types.normalizeUnion(shape, raw, depth)
	default:
		return nil, fmt.Errorf("manifest contains an unknown shape kind %q", shape.Kind)
	}
}

func (types ManifestTypes) normalizeRecord(shape ManifestShape, raw []byte, depth int) ([]byte, error) {
	fields, err := DecodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(fields))
	for _, field := range shape.Fields {
		t, err := parseContractWireType(field.Type)
		if err != nil {
			return nil, err
		}
		input, present := fields[field.Name]
		if !present {
			input = field.Default
		}
		if len(input) == 0 && t.name == "optional" {
			continue
		}
		encoded, err := types.normalize(t, input, field.Constraints, depth+1)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		result[field.Name] = encoded
		delete(fields, field.Name)
	}
	for name, raw := range fields {
		switch shape.UnknownFields {
		case "ignore":
		case "preserve":
			encoded, err := canonicalizeExactJSON(raw)
			if err != nil {
				return nil, err
			}
			result[name] = encoded
		default:
			return nil, fmt.Errorf("unknown record field %s", name)
		}
	}
	return joinContractJSONObject(result)
}

func (types ManifestTypes) normalizeUnion(shape ManifestShape, raw []byte, depth int) ([]byte, error) {
	fields, err := DecodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var tag string
	tagAtom := fields[shape.Discriminator]
	if err := UnmarshalContractValue(tagAtom, &tag, "string"); err != nil {
		return nil, fmt.Errorf("union discriminator is missing or invalid")
	}
	variant, known := shape.Variants[tag]
	if !known {
		if shape.Open {
			return canonicalizeExactJSON(raw)
		}
		return nil, fmt.Errorf("unknown union discriminator")
	}
	delete(fields, shape.Discriminator)
	data := make(map[string][]byte, len(fields))
	for name, value := range fields {
		data[name] = value
	}
	body, err := joinContractJSONObject(data)
	if err != nil {
		return nil, err
	}
	t, err := parseContractWireType(variant)
	if err != nil {
		return nil, err
	}
	validated, err := types.normalize(t, body, ContractConstraints{}, depth+1)
	if err != nil {
		return nil, err
	}
	fields, err = DecodeJSONObject(validated)
	if err != nil {
		return nil, err
	}
	clear(data)
	for name, value := range fields {
		data[name] = value
	}
	data[shape.Discriminator], _ = json.Marshal(tag)
	return joinContractJSONObject(data)
}

func normalizeManifestScalar(t contractWireType, raw []byte, constraints ContractConstraints) ([]byte, error) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("null requires a nullable type")
	}
	var target any
	switch t.name {
	case "bool":
		target = new(bool)
	case "string":
		target = new(string)
	case "bytes":
		target = new([]byte)
	case "int32":
		target = new(int32)
	case "uint32":
		target = new(uint32)
	case "int64":
		target = new(int64)
	case "uint64":
		target = new(uint64)
	case "float32":
		target = new(float32)
	case "float64":
		target = new(float64)
	case "decimal":
		target = new(Decimal)
	case "timestamp":
		target = new(time.Time)
	case "duration":
		target = new(Duration)
	case "std.type.unit":
		target = new(Unit)
	case "std.type.problem":
		target = new(Problem)
	default:
		return nil, fmt.Errorf("manifest has no wire type %s", t.name)
	}
	value := reflect.ValueOf(target).Elem()
	if err := unmarshalContractReflect(raw, value, t, nil); err != nil {
		return nil, err
	}
	if err := validateContractReflect(value, t, constraints); err != nil {
		return nil, err
	}
	return marshalContractReflect(value, t)
}

// EncodeManifestOutcome assembles an outcome from an already normalized
// payload without changing its schema-directed numeric representation.
func EncodeManifestOutcome(kind, name string, raw []byte) ([]byte, error) {
	payload := "value"
	if kind == "error" {
		payload = "problem"
	}
	if (kind != "result" && kind != "error") || name == "" || len(raw) == 0 {
		return nil, fmt.Errorf("invalid manifest outcome")
	}
	kindJSON, _ := json.Marshal(kind)
	nameJSON, _ := json.Marshal(name)
	return joinContractJSONObject(map[string][]byte{"kind": kindJSON, "name": nameJSON, payload: raw})
}
