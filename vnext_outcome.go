package scenery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MarshalContractOutcomeVariant encodes one closed operation outcome using a
// stable envelope suitable for durable storage and wait delivery.
func MarshalContractOutcomeVariant(kind, name string, value any, typeExpression string) ([]byte, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	if kind != "result" && kind != "error" {
		return nil, fmt.Errorf("contract outcome kind must be result or error")
	}
	if name == "" {
		return nil, fmt.Errorf("contract outcome name is required")
	}
	payload, err := MarshalContractValue(value, typeExpression)
	if err != nil {
		return nil, fmt.Errorf("encode contract outcome %s.%s: %w", kind, name, err)
	}
	kindJSON, _ := json.Marshal(kind)
	nameJSON, _ := json.Marshal(name)
	payloadName := "value"
	if kind == "error" {
		payloadName = "problem"
	}
	return joinContractJSONObject(map[string][]byte{
		"kind": kindJSON, "name": nameJSON, payloadName: payload,
	})
}

// DecodeContractOutcomeEnvelope validates a closed durable outcome envelope
// and returns a defensive copy of its schema-directed payload.
func DecodeContractOutcomeEnvelope(data []byte) (kind, name string, payload json.RawMessage, err error) {
	object, err := DecodeJSONObject(data)
	if err != nil {
		return "", "", nil, err
	}
	if err := UnmarshalContractValue(object["kind"], &kind, "string"); err != nil {
		return "", "", nil, fmt.Errorf("decode contract outcome kind: %w", err)
	}
	if err := UnmarshalContractValue(object["name"], &name, "string"); err != nil {
		return "", "", nil, fmt.Errorf("decode contract outcome name: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		return "", "", nil, fmt.Errorf("contract outcome name is required")
	}
	payloadName := ""
	switch kind {
	case "result":
		payloadName = "value"
	case "error":
		payloadName = "problem"
	default:
		return "", "", nil, fmt.Errorf("unknown contract outcome kind %q", kind)
	}
	payload, exists := object[payloadName]
	if !exists {
		return "", "", nil, fmt.Errorf("contract outcome %s.%s is missing %s", kind, name, payloadName)
	}
	if len(object) != 3 {
		return "", "", nil, fmt.Errorf("contract outcome envelope has unknown members")
	}
	return kind, name, append(json.RawMessage(nil), payload...), nil
}
