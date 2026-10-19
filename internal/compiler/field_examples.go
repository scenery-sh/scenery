package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// fieldExamplesWire validates a record field's authored examples against the
// field type and constraints and returns each example in the exact JSON wire
// form a client sends, so generated interfaces can show it unchanged.
func fieldExamplesWire(field map[string]any, module string, resources map[string]Resource) ([]any, error) {
	examples, ok := field["examples"].([]any)
	if !ok {
		return nil, fmt.Errorf("examples must be a list")
	}
	typeText := typeExpressionText(field["type"])
	wire := make([]any, 0, len(examples))
	for index, example := range examples {
		semantic, err := contextualizeValue(example, typeText, module, resources)
		if err != nil {
			return nil, fmt.Errorf("example %d: %w", index, err)
		}
		if err := validateFixtureFieldValue(semantic, field, module, resources); err != nil {
			return nil, fmt.Errorf("example %d: %w", index, err)
		}
		value, err := exampleWireValue(semantic, typeText, module, resources)
		if err != nil {
			return nil, fmt.Errorf("example %d: %w", index, err)
		}
		wire = append(wire, value)
	}
	return wire, nil
}

// exampleWireValue encodes a validated semantic value as contract wire JSON.
func exampleWireValue(value any, typeExpression, module string, resources map[string]Resource) (any, error) {
	typeExpression = strings.TrimSpace(typeExpression)
	for _, wrapper := range []string{"optional", "nullable"} {
		if inner, ok := wrappedFixtureType(typeExpression, wrapper); ok {
			if value == nil {
				return nil, nil
			}
			return exampleWireValue(value, inner, module, resources)
		}
	}
	for _, wrapper := range []string{"list", "set"} {
		if inner, ok := wrappedFixtureType(typeExpression, wrapper); ok {
			items, _ := value.([]any)
			result := make([]any, 0, len(items))
			for _, item := range items {
				converted, err := exampleWireValue(item, inner, module, resources)
				if err != nil {
					return nil, err
				}
				result = append(result, converted)
			}
			return result, nil
		}
	}
	if inner, ok := wrappedFixtureType(typeExpression, "map"); ok {
		object, _ := value.(map[string]any)
		result := make(map[string]any, len(object))
		for key, item := range object {
			converted, err := exampleWireValue(item, inner, module, resources)
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	}
	if strings.HasPrefix(typeExpression, "tuple(") && strings.HasSuffix(typeExpression, ")") {
		items, _ := value.([]any)
		arguments := splitTypeArguments(typeExpression[len("tuple(") : len(typeExpression)-1])
		result := make([]any, 0, len(items))
		for index, item := range items {
			converted, err := exampleWireValue(item, arguments[index], module, resources)
			if err != nil {
				return nil, err
			}
			result = append(result, converted)
		}
		return result, nil
	}
	if resource := resources[namedFixtureTypeAddress(typeExpression, module)]; resource.Address != "" {
		switch resource.Kind {
		case "scenery.record":
			return exampleRecordWire(value, resource, resources)
		case "scenery.enum":
			candidate := stringValue(value)
			if reference := refString(value); reference != "" {
				candidate = lastRef(reference)
			}
			for _, item := range namedChildren(resource.Spec, "value") {
				name := stringValue(item["name"])
				if candidate == name || candidate == wireName(item, name) {
					if wire := stringValue(item["wire_value"]); wire != "" {
						return wire, nil
					}
					return name, nil
				}
			}
			return nil, fmt.Errorf("value is not declared by enum %s", resource.Address)
		case "scenery.union":
			return exampleUnionWire(value, resource, resources)
		}
	}
	if typeExpression == "json" {
		return value, nil
	}
	encoded, err := ConfigWireJSON(value, typeExpression)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var wire any
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	return wire, nil
}

func exampleRecordWire(value any, record Resource, resources map[string]Resource) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("record value must be an object")
	}
	result := make(map[string]any, len(object))
	for _, field := range namedChildren(record.Spec, "field") {
		name := stringValue(field["name"])
		item, present := object[name]
		if !present {
			continue
		}
		converted, err := exampleWireValue(item, typeExpressionText(field["type"]), record.Module, resources)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
		result[wireName(field, name)] = converted
	}
	return result, nil
}

// exampleUnionWire validates a tagged-union example by its declared variant
// and returns the flattened wire object with the variant's wire tag.
func exampleUnionWire(value any, union Resource, resources map[string]Resource) (map[string]any, error) {
	discriminator := stringValue(union.Spec["discriminator"])
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("union value must be an object")
	}
	tag := stringValue(object[discriminator])
	for _, variant := range namedChildren(union.Spec, "variant") {
		name := stringValue(variant["name"])
		wireTag := stringValue(variant["wire_tag"])
		if wireTag == "" {
			wireTag = name
		}
		if tag != name && tag != wireTag {
			continue
		}
		payload := cloneMapValue(object)
		delete(payload, discriminator)
		recordType := typeExpressionText(variant["type"])
		record := resources[namedFixtureTypeAddress(recordType, union.Module)]
		if record.Kind != "scenery.record" {
			return nil, fmt.Errorf("union variant %s has no record payload", name)
		}
		contextual, err := contextualizeRecordValue(payload, record, resources)
		if err != nil {
			return nil, fmt.Errorf("variant %s: %w", name, err)
		}
		if err := validateFixtureValue(contextual, recordType, union.Module, resources); err != nil {
			return nil, fmt.Errorf("variant %s: %w", name, err)
		}
		wire, err := exampleRecordWire(contextual, record, resources)
		if err != nil {
			return nil, fmt.Errorf("variant %s: %w", name, err)
		}
		wire[discriminator] = wireTag
		return wire, nil
	}
	return nil, fmt.Errorf("union %s has no variant %q", union.Address, tag)
}
