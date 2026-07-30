package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
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

// contractValidationProgramCache memoizes decoded validation programs. The
// encoded program is a constant in the generated contract package, so a request
// that validates a record would otherwise re-decode the same JSON every call.
// Programs are read-only during evaluation, so sharing one is safe.
var contractValidationProgramCache sync.Map // encoded program -> cachedContractValidationProgram

type cachedContractValidationProgram struct {
	program contractValidationProgram
	err     error
}

func parseContractValidationProgram(encodedProgram string) (contractValidationProgram, error) {
	if cached, ok := contractValidationProgramCache.Load(encodedProgram); ok {
		if entry, valid := cached.(cachedContractValidationProgram); valid {
			return entry.program, entry.err
		}
	}
	decoder := json.NewDecoder(bytes.NewBufferString(encodedProgram))
	decoder.DisallowUnknownFields()
	var program contractValidationProgram
	err := error(nil)
	if decodeErr := decoder.Decode(&program); decodeErr != nil {
		err = fmt.Errorf("decode compiled validation expression: %w", decodeErr)
	} else if program.Source == "" || program.Expression.Kind == "" {
		err = fmt.Errorf("decode compiled validation expression: source or expression is absent")
	}
	contractValidationProgramCache.Store(encodedProgram, cachedContractValidationProgram{program: program, err: err})
	return program, err
}

// ValidateContractRecord evaluates a compiler-validated, data-only expression
// over generated record fields. The public runtime deliberately does not
// include the HCL compiler; generated packages carry the compiled expression.
func ValidateContractRecord(fields map[string]any, fieldTypes map[string]string, encodedProgram, code, message, path string) error {
	program, err := parseContractValidationProgram(encodedProgram)
	if err != nil {
		return err
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
