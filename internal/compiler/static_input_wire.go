package compiler

import "encoding/json"

// StaticOperationInputJSON projects a declared invocation input to the same
// wire representation used by dynamically supplied inputs.
func StaticOperationInputJSON(resources map[string]Resource, operation Resource, value any) ([]byte, error) {
	expression := typeExpression(operation.Spec["input"])
	if err := validateFixtureValue(value, expression, operation.Module, resources); err != nil {
		return nil, err
	}
	wire, err := exampleWireValue(value, expression, operation.Module, resources)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}
