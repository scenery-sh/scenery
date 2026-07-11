package scenery

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// EncodeContractKeyComponent preserves the schema type and presence state of
// one idempotency or concurrency-key component.
func EncodeContractKeyComponent(value any, typeExpression string) ([]byte, error) {
	typeValue, err := parseContractWireType(typeExpression)
	if err != nil {
		return nil, err
	}
	state := "value"
	reflected := reflect.ValueOf(value)
	switch typeValue.name {
	case "optional":
		reflected = indirectContractKeyValue(reflected)
		if !reflected.IsValid() || reflected.Kind() != reflect.Struct || !reflected.FieldByName("Set").IsValid() {
			return nil, fmt.Errorf("optional key component has invalid representation")
		}
		if !reflected.FieldByName("Set").Bool() {
			state = "absent"
		}
	case "nullable":
		reflected = indirectContractKeyValue(reflected)
		if !reflected.IsValid() || reflected.Kind() != reflect.Struct || !reflected.FieldByName("Null").IsValid() {
			return nil, fmt.Errorf("nullable key component has invalid representation")
		}
		if reflected.FieldByName("Null").Bool() {
			state = "null"
		}
	default:
		if !indirectContractKeyValue(reflected).IsValid() {
			return nil, fmt.Errorf("non-nullable key component is nil")
		}
	}
	typeJSON, _ := json.Marshal(strings.TrimSpace(typeExpression))
	stateJSON, _ := json.Marshal(state)
	members := map[string][]byte{"state": stateJSON, "type": typeJSON}
	if state == "value" {
		encoded, err := MarshalContractValue(value, typeExpression)
		if err != nil {
			return nil, fmt.Errorf("encode contract key component: %w", err)
		}
		members["value"] = encoded
	}
	return joinContractJSONObject(members)
}

func EncodeContractCompositeKey(components ...[]byte) (string, error) {
	if len(components) == 0 {
		return "", fmt.Errorf("contract composite key requires at least one component")
	}
	canonical := make([][]byte, len(components))
	for index, component := range components {
		object, err := DecodeJSONObject(component)
		if err != nil {
			return "", fmt.Errorf("decode contract key component %d: %w", index, err)
		}
		var state, typeExpression string
		if err := UnmarshalContractValue(object["state"], &state, "string"); err != nil {
			return "", fmt.Errorf("decode contract key component %d state: %w", index, err)
		}
		if err := UnmarshalContractValue(object["type"], &typeExpression, "string"); err != nil || strings.TrimSpace(typeExpression) == "" {
			return "", fmt.Errorf("decode contract key component %d type", index)
		}
		expectedMembers := 2
		switch state {
		case "value":
			expectedMembers = 3
			if object["value"] == nil {
				return "", fmt.Errorf("contract key component %d is missing value", index)
			}
		case "absent":
			if !strings.HasPrefix(typeExpression, "optional(") {
				return "", fmt.Errorf("contract key component %d has invalid absent state", index)
			}
		case "null":
			if !strings.HasPrefix(typeExpression, "nullable(") {
				return "", fmt.Errorf("contract key component %d has invalid null state", index)
			}
		default:
			return "", fmt.Errorf("contract key component %d has unknown state %q", index, state)
		}
		if len(object) != expectedMembers {
			return "", fmt.Errorf("contract key component %d has unknown members", index)
		}
		canonical[index], err = canonicalizeExactJSON(component)
		if err != nil {
			return "", err
		}
	}
	encoded := joinContractJSONArray(canonical)
	return "scenery-key-v1." + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func indirectContractKeyValue(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}
