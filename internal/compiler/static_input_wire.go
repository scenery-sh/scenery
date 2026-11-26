package compiler

import "encoding/json"

// StaticOperationInputJSON projects a declared invocation input to the same
// wire representation used by dynamically supplied inputs.
func StaticOperationInputJSON(resources map[string]Resource, operation Resource, value any) ([]byte, error) {
	expression := typeExpression(operation.Spec["input"])
	if err := validateFixtureValue(value, expression, operation.Module, resources); err != nil {
		return nil, err
	}
	return staticWireJSON(resources, operation.Module, expression, value)
}

// StaticFieldDefaultJSON projects a compiled record default into contract wire JSON.
func StaticFieldDefaultJSON(resources map[string]Resource, record Resource, field map[string]any) ([]byte, error) {
	expression := typeExpressionText(field["type"])
	value := field["default"]
	if inner, optional := wrappedFixtureType(expression, "optional"); optional {
		expression = inner
	}
	if err := validateFixtureValue(value, expression, record.Module, resources); err != nil {
		return nil, err
	}
	return staticWireJSON(resources, record.Module, expression, value)
}

func staticWireJSON(resources map[string]Resource, module, expression string, value any) ([]byte, error) {
	wire, err := exampleWireValue(value, expression, module, resources)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}
