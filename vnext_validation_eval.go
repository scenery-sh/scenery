package scenery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

func evaluateContractValidation(node contractValidationNode, record map[string]any) (any, error) {
	switch node.Kind {
	case "literal":
		return contractValidationLiteral(node)
	case "value":
		return record, nil
	case "attribute":
		if node.Source == nil {
			return nil, fmt.Errorf("attribute source is absent")
		}
		source, err := evaluateContractValidation(*node.Source, record)
		if err != nil {
			return nil, err
		}
		return contractValidationAttribute(source, node.Name)
	case "index":
		if node.Collection == nil || node.Key == nil {
			return nil, fmt.Errorf("index operands are absent")
		}
		collection, err := evaluateContractValidation(*node.Collection, record)
		if err != nil {
			return nil, err
		}
		key, err := evaluateContractValidation(*node.Key, record)
		if err != nil {
			return nil, err
		}
		return contractValidationIndex(collection, key)
	case "call":
		arguments := make([]any, len(node.Arguments))
		for index, argument := range node.Arguments {
			value, err := evaluateContractValidation(argument, record)
			if err != nil {
				return nil, err
			}
			arguments[index] = value
		}
		return contractValidationCall(node.Name, arguments)
	case "unary":
		if node.Value == nil {
			return nil, fmt.Errorf("unary operand is absent")
		}
		var operand contractValidationNode
		if err := json.Unmarshal(node.Value, &operand); err != nil {
			return nil, err
		}
		value, err := evaluateContractValidation(operand, record)
		if err != nil {
			return nil, err
		}
		if node.Operator == "!" {
			boolean, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("logical operand is not bool")
			}
			return !boolean, nil
		}
		if node.Operator == "-" {
			number, err := contractValidationRequireNumber(value)
			if err != nil {
				return nil, err
			}
			return new(big.Rat).Neg(number), nil
		}
		return nil, fmt.Errorf("unknown unary operator %q", node.Operator)
	case "binary":
		return evaluateContractValidationBinary(node, record)
	case "conditional":
		if node.Condition == nil || node.TrueResult == nil || node.FalseResult == nil {
			return nil, fmt.Errorf("conditional operand is absent")
		}
		condition, err := evaluateContractValidation(*node.Condition, record)
		if err != nil {
			return nil, err
		}
		boolean, ok := condition.(bool)
		if !ok {
			return nil, fmt.Errorf("conditional predicate is not bool")
		}
		if boolean {
			return evaluateContractValidation(*node.TrueResult, record)
		}
		return evaluateContractValidation(*node.FalseResult, record)
	case "tuple":
		values := make([]any, len(node.Values))
		for index, item := range node.Values {
			value, err := evaluateContractValidation(item, record)
			if err != nil {
				return nil, err
			}
			values[index] = value
		}
		return values, nil
	case "object":
		value := make(map[string]any, len(node.Entries))
		for _, entry := range node.Entries {
			key, err := evaluateContractValidation(entry.Key, record)
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not string")
			}
			item, err := evaluateContractValidation(entry.Value, record)
			if err != nil {
				return nil, err
			}
			value[name] = item
		}
		return value, nil
	case "template":
		var result strings.Builder
		for _, part := range node.Parts {
			value, err := evaluateContractValidation(part, record)
			if err != nil {
				return nil, err
			}
			text, err := contractValidationString(value)
			if err != nil {
				return nil, err
			}
			result.WriteString(text)
		}
		return result.String(), nil
	default:
		return nil, fmt.Errorf("unknown validation node %q", node.Kind)
	}
}

func contractValidationLiteral(node contractValidationNode) (any, error) {
	switch node.Type {
	case "null":
		return nil, nil
	case "bool":
		var value bool
		return value, json.Unmarshal(node.Value, &value)
	case "string":
		var value string
		return value, json.Unmarshal(node.Value, &value)
	case "number":
		var value string
		if err := json.Unmarshal(node.Value, &value); err != nil {
			return nil, err
		}
		return contractValidationNumber(value)
	default:
		return nil, fmt.Errorf("unknown literal type %q", node.Type)
	}
}

func evaluateContractValidationBinary(node contractValidationNode, record map[string]any) (any, error) {
	if node.Left == nil || node.Right == nil {
		return nil, fmt.Errorf("binary operand is absent")
	}
	left, err := evaluateContractValidation(*node.Left, record)
	if err != nil {
		return nil, err
	}
	if node.Operator == "&&" || node.Operator == "||" {
		boolean, ok := left.(bool)
		if !ok {
			return nil, fmt.Errorf("logical operand is not bool")
		}
		if node.Operator == "&&" && !boolean || node.Operator == "||" && boolean {
			return boolean, nil
		}
	}
	right, err := evaluateContractValidation(*node.Right, record)
	if err != nil {
		return nil, err
	}
	switch node.Operator {
	case "&&", "||":
		value, ok := right.(bool)
		if !ok {
			return nil, fmt.Errorf("logical operand is not bool")
		}
		return value, nil
	case "==":
		return contractValidationEqual(left, right), nil
	case "!=":
		return !contractValidationEqual(left, right), nil
	case "<", "<=", ">", ">=":
		comparison, err := contractValidationCompare(left, right)
		if err != nil {
			return nil, err
		}
		switch node.Operator {
		case "<":
			return comparison < 0, nil
		case "<=":
			return comparison <= 0, nil
		case ">":
			return comparison > 0, nil
		default:
			return comparison >= 0, nil
		}
	case "+", "-", "*", "/", "%":
		return contractValidationArithmetic(node.Operator, left, right)
	default:
		return nil, fmt.Errorf("unknown binary operator %q", node.Operator)
	}
}

func contractValidationCall(name string, arguments []any) (any, error) {
	switch name {
	case "contains":
		if len(arguments) != 2 {
			return nil, fmt.Errorf("contains requires two arguments")
		}
		if text, ok := arguments[0].(string); ok {
			needle, ok := arguments[1].(string)
			if !ok {
				return nil, fmt.Errorf("contains string needle is not string")
			}
			return strings.Contains(text, needle), nil
		}
		if values, ok := arguments[0].([]any); ok {
			for _, value := range values {
				if contractValidationEqual(value, arguments[1]) {
					return true, nil
				}
			}
			return false, nil
		}
		return nil, fmt.Errorf("contains requires a string or collection")
	case "length":
		if len(arguments) != 1 {
			return nil, fmt.Errorf("length requires one argument")
		}
		var length int
		switch value := arguments[0].(type) {
		case string:
			length = utf8.RuneCountInString(value)
		case []any:
			length = len(value)
		case map[string]any:
			length = len(value)
		default:
			return nil, fmt.Errorf("length requires a string or collection")
		}
		return new(big.Rat).SetInt64(int64(length)), nil
	case "starts_with", "ends_with":
		if len(arguments) != 2 {
			return nil, fmt.Errorf("%s requires two arguments", name)
		}
		value, valueOK := arguments[0].(string)
		affix, affixOK := arguments[1].(string)
		if !valueOK || !affixOK {
			return nil, fmt.Errorf("%s requires two strings", name)
		}
		if name == "starts_with" {
			return strings.HasPrefix(value, affix), nil
		}
		return strings.HasSuffix(value, affix), nil
	case "lower", "upper":
		if len(arguments) != 1 {
			return nil, fmt.Errorf("%s requires one argument", name)
		}
		value, ok := arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("%s requires a string", name)
		}
		if name == "lower" {
			return strings.ToLower(value), nil
		}
		return strings.ToUpper(value), nil
	case "format":
		if len(arguments) == 0 {
			return nil, fmt.Errorf("format requires a format string")
		}
		format, ok := arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("format requires a format string")
		}
		values := make([]any, len(arguments)-1)
		for index, value := range arguments[1:] {
			if number, ok := value.(*big.Rat); ok {
				if number.IsInt() {
					values[index] = number.Num()
				} else {
					values[index] = number.FloatString(32)
				}
			} else {
				values[index] = value
			}
		}
		return fmt.Sprintf(format, values...), nil
	default:
		return nil, fmt.Errorf("unknown validation function %q", name)
	}
}

func contractValidationAttribute(source any, name string) (any, error) {
	if object, ok := source.(map[string]any); ok {
		value, exists := object[name]
		if !exists {
			return nil, fmt.Errorf("attribute %q is absent", name)
		}
		return value, nil
	}
	value := reflect.ValueOf(source)
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil, fmt.Errorf("attribute %q source is null", name)
		}
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Struct {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			return field.Interface(), nil
		}
	}
	return nil, fmt.Errorf("attribute %q source is not an object", name)
}

func contractValidationIndex(collection, key any) (any, error) {
	switch value := collection.(type) {
	case []any:
		index, err := contractValidationInteger(key)
		if err != nil || index < 0 || index >= int64(len(value)) {
			return nil, fmt.Errorf("collection index is out of range")
		}
		return value[index], nil
	case map[string]any:
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("map key is not string")
		}
		item, exists := value[name]
		if !exists {
			return nil, fmt.Errorf("map key %q is absent", name)
		}
		return item, nil
	default:
		return nil, fmt.Errorf("index source is not a collection")
	}
}

func contractValidationCompare(left, right any) (int, error) {
	if leftNumber, ok := left.(*big.Rat); ok {
		rightNumber, ok := right.(*big.Rat)
		if !ok {
			return 0, fmt.Errorf("comparison operands have incompatible types")
		}
		return leftNumber.Cmp(rightNumber), nil
	}
	leftString, leftOK := left.(string)
	rightString, rightOK := right.(string)
	if leftOK && rightOK {
		return strings.Compare(leftString, rightString), nil
	}
	return 0, fmt.Errorf("comparison operands have incompatible types")
}

func contractValidationEqual(left, right any) bool {
	if leftNumber, ok := left.(*big.Rat); ok {
		rightNumber, ok := right.(*big.Rat)
		return ok && leftNumber.Cmp(rightNumber) == 0
	}
	leftList, leftListOK := left.([]any)
	rightList, rightListOK := right.([]any)
	if leftListOK || rightListOK {
		if !leftListOK || !rightListOK || len(leftList) != len(rightList) {
			return false
		}
		for index := range leftList {
			if !contractValidationEqual(leftList[index], rightList[index]) {
				return false
			}
		}
		return true
	}
	leftMap, leftMapOK := left.(map[string]any)
	rightMap, rightMapOK := right.(map[string]any)
	if leftMapOK || rightMapOK {
		if !leftMapOK || !rightMapOK || len(leftMap) != len(rightMap) {
			return false
		}
		for key, value := range leftMap {
			if !contractValidationEqual(value, rightMap[key]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}

func contractValidationArithmetic(operator string, left, right any) (any, error) {
	leftNumber, err := contractValidationRequireNumber(left)
	if err != nil {
		return nil, err
	}
	rightNumber, err := contractValidationRequireNumber(right)
	if err != nil {
		return nil, err
	}
	switch operator {
	case "+":
		return new(big.Rat).Add(leftNumber, rightNumber), nil
	case "-":
		return new(big.Rat).Sub(leftNumber, rightNumber), nil
	case "*":
		return new(big.Rat).Mul(leftNumber, rightNumber), nil
	case "/":
		if rightNumber.Sign() == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return new(big.Rat).Quo(leftNumber, rightNumber), nil
	case "%":
		if rightNumber.Sign() == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		quotient := new(big.Rat).Quo(leftNumber, rightNumber)
		integer := new(big.Int).Quo(quotient.Num(), quotient.Denom())
		return new(big.Rat).Sub(leftNumber, new(big.Rat).Mul(new(big.Rat).SetInt(integer), rightNumber)), nil
	default:
		return nil, fmt.Errorf("unknown arithmetic operator %q", operator)
	}
}

func contractValidationRequireNumber(value any) (*big.Rat, error) {
	number, ok := value.(*big.Rat)
	if !ok {
		return nil, fmt.Errorf("numeric operand is not a number")
	}
	return number, nil
}

func contractValidationInteger(value any) (int64, error) {
	number, err := contractValidationRequireNumber(value)
	if err != nil || !number.IsInt() || !number.Num().IsInt64() {
		return 0, fmt.Errorf("value is not an integer")
	}
	return number.Num().Int64(), nil
}

func contractValidationNumber(source string) (*big.Rat, error) {
	mantissa, exponentText := source, "0"
	if index := strings.IndexAny(source, "eE"); index >= 0 {
		mantissa, exponentText = source[:index], source[index+1:]
	}
	exponent, err := strconv.Atoi(exponentText)
	if err != nil || exponent < -1_000_000 || exponent > 1_000_000 {
		return nil, fmt.Errorf("invalid validation number %q", source)
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("invalid validation number %q", source)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	digits := parts[0] + fraction
	coefficient := new(big.Int)
	if _, ok := coefficient.SetString(digits, 10); !ok {
		return nil, fmt.Errorf("invalid validation number %q", source)
	}
	if negative {
		coefficient.Neg(coefficient)
	}
	scale := len(fraction) - exponent
	if scale <= 0 {
		coefficient.Mul(coefficient, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil))
		return new(big.Rat).SetInt(coefficient), nil
	}
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	return new(big.Rat).SetFrac(coefficient, denominator), nil
}

func contractValidationString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case *big.Rat:
		if typed.IsInt() {
			return typed.Num().String(), nil
		}
		return typed.RatString(), nil
	default:
		return "", fmt.Errorf("value cannot convert to string")
	}
}

func contractValidationGenericValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case json.Number:
		return contractValidationNumber(typed.String())
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			converted, err := contractValidationGenericValue(item)
			if err != nil {
				return nil, err
			}
			items[index] = converted
		}
		return items, nil
	case map[string]any:
		items := make(map[string]any, len(typed))
		for key, item := range typed {
			converted, err := contractValidationGenericValue(item)
			if err != nil {
				return nil, err
			}
			items[key] = converted
		}
		return items, nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var generic any
		if err := decoder.Decode(&generic); err != nil {
			return nil, err
		}
		return contractValidationGenericValue(generic)
	}
}
