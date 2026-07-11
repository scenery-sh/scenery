package scenery

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ContractValidationError is a declared record-validation failure. Code,
// message, and path come from the named validation block in the contract.
type ContractValidationError struct {
	Code    string
	Message string
	Path    string
}

func (err *ContractValidationError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s at %s: %s", err.Code, err.Path, err.Message)
}

type contractValidationProgram struct {
	Source     string                 `json:"source"`
	Expression contractValidationNode `json:"expression"`
}

type contractValidationNode struct {
	Kind        string                    `json:"kind"`
	Type        string                    `json:"type,omitempty"`
	Value       json.RawMessage           `json:"value,omitempty"`
	Source      *contractValidationNode   `json:"source,omitempty"`
	Name        string                    `json:"name,omitempty"`
	Collection  *contractValidationNode   `json:"collection,omitempty"`
	Key         *contractValidationNode   `json:"key,omitempty"`
	Arguments   []contractValidationNode  `json:"arguments,omitempty"`
	Operator    string                    `json:"operator,omitempty"`
	Left        *contractValidationNode   `json:"left,omitempty"`
	Right       *contractValidationNode   `json:"right,omitempty"`
	Condition   *contractValidationNode   `json:"condition,omitempty"`
	TrueResult  *contractValidationNode   `json:"true_result,omitempty"`
	FalseResult *contractValidationNode   `json:"false_result,omitempty"`
	Values      []contractValidationNode  `json:"values,omitempty"`
	Entries     []contractValidationEntry `json:"entries,omitempty"`
	Parts       []contractValidationNode  `json:"parts,omitempty"`
}

type contractValidationEntry struct {
	Key   contractValidationNode `json:"key"`
	Value contractValidationNode `json:"value"`
}

// ValidateContractRecord evaluates a compiler-validated, data-only expression
// over generated record fields. The public runtime deliberately does not
// include the HCL compiler; generated packages carry the compiled expression.
func ValidateContractRecord(fields map[string]any, fieldTypes map[string]string, encodedProgram, code, message, path string) error {
	decoder := json.NewDecoder(bytes.NewBufferString(encodedProgram))
	decoder.DisallowUnknownFields()
	var program contractValidationProgram
	if err := decoder.Decode(&program); err != nil {
		return fmt.Errorf("decode compiled validation expression: %w", err)
	}
	if program.Source == "" || program.Expression.Kind == "" {
		return fmt.Errorf("decode compiled validation expression: source or expression is absent")
	}
	values := make(map[string]any, len(fieldTypes))
	for name, typeExpression := range fieldTypes {
		value, err := contractValidationValue(fields[name], typeExpression)
		if err != nil {
			return fmt.Errorf("validation field %s: %w", name, err)
		}
		values[name] = value
	}
	result, err := evaluateContractValidation(program.Expression, values)
	if err != nil {
		return fmt.Errorf("evaluate validation expression %q: %w", program.Source, err)
	}
	failed, ok := result.(bool)
	if !ok {
		return fmt.Errorf("evaluate validation expression %q: result is not bool", program.Source)
	}
	if failed {
		return &ContractValidationError{Code: code, Message: message, Path: path}
	}
	return nil
}
