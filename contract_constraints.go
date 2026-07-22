package scenery

import (
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"unicode/utf8"
)

// ContractConstraints is emitted by generated contract packages. Pointer
// fields distinguish an absent limit from an explicit zero limit.
type ContractConstraints struct {
	Minimum     *string
	Maximum     *string
	MinLength   *int64
	MaxLength   *int64
	Pattern     string
	Format      string
	MinItems    *int64
	MaxItems    *int64
	UniqueItems bool
}

func ContractStringConstraint(value string) *string { return &value }
func ContractIntConstraint(value int64) *int64      { return &value }

// ValidateContractValue enforces one field's contract constraints against
// the generated Go representation.
func ValidateContractValue(value any, typeExpression string, constraints ContractConstraints) error {
	typeValue, err := parseContractWireType(typeExpression)
	if err != nil {
		return err
	}
	return validateContractReflect(reflect.ValueOf(value), typeValue, constraints)
}

func validateContractReflect(value reflect.Value, typeValue contractWireType, constraints ContractConstraints) error {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if typeValue.name == "optional" {
		if !value.IsValid() || value.FieldByName("Set").IsValid() && !value.FieldByName("Set").Bool() {
			return nil
		}
		return validateContractReflect(value.FieldByName("Value"), typeValue.args[0], constraints)
	}
	if typeValue.name == "nullable" {
		if !value.IsValid() || value.FieldByName("Null").IsValid() && value.FieldByName("Null").Bool() {
			return nil
		}
		return validateContractReflect(value.FieldByName("Value"), typeValue.args[0], constraints)
	}

	if constraints.Minimum != nil || constraints.Maximum != nil {
		number, ok := contractConstraintNumber(value, typeValue.name)
		if !ok {
			return fmt.Errorf("numeric constraints cannot validate %s", typeValue.name)
		}
		if constraints.Minimum != nil {
			minimum, ok := new(big.Rat).SetString(*constraints.Minimum)
			if !ok || number.Cmp(minimum) < 0 {
				return fmt.Errorf("value is less than minimum %s", *constraints.Minimum)
			}
		}
		if constraints.Maximum != nil {
			maximum, ok := new(big.Rat).SetString(*constraints.Maximum)
			if !ok || number.Cmp(maximum) > 0 {
				return fmt.Errorf("value exceeds maximum %s", *constraints.Maximum)
			}
		}
	}

	length, hasLength := contractValueLength(value, typeValue.name)
	if constraints.MinLength != nil {
		if !hasLength || length < *constraints.MinLength {
			return fmt.Errorf("value length is less than %d", *constraints.MinLength)
		}
	}
	if constraints.MaxLength != nil {
		if !hasLength || length > *constraints.MaxLength {
			return fmt.Errorf("value length exceeds %d", *constraints.MaxLength)
		}
	}
	if constraints.MinItems != nil {
		if !hasLength || length < *constraints.MinItems {
			return fmt.Errorf("item count is less than %d", *constraints.MinItems)
		}
	}
	if constraints.MaxItems != nil {
		if !hasLength || length > *constraints.MaxItems {
			return fmt.Errorf("item count exceeds %d", *constraints.MaxItems)
		}
	}
	if constraints.Pattern != "" {
		if value.Kind() != reflect.String {
			return fmt.Errorf("pattern requires a string value")
		}
		pattern, err := regexp.Compile(constraints.Pattern)
		if err != nil || !pattern.MatchString(value.String()) {
			return fmt.Errorf("value does not match pattern %q", constraints.Pattern)
		}
	}
	if constraints.Format != "" {
		if value.Kind() != reflect.String {
			return fmt.Errorf("format requires a string value")
		}
		if err := validateContractStringFormat(value.String(), constraints.Format); err != nil {
			return err
		}
	}
	if constraints.UniqueItems && (typeValue.name == "list" || typeValue.name == "set") {
		seen := map[string]bool{}
		for index := 0; index < value.Len(); index++ {
			encoded, err := marshalContractReflect(value.Index(index), typeValue.args[0])
			if err != nil {
				return err
			}
			key := string(encoded)
			if seen[key] {
				return fmt.Errorf("duplicate collection element")
			}
			seen[key] = true
		}
	}
	return nil
}

func contractConstraintNumber(value reflect.Value, typeName string) (*big.Rat, bool) {
	if !value.IsValid() {
		return nil, false
	}
	var source string
	switch typeName {
	case "int":
		integer, ok := value.Interface().(Int)
		if !ok {
			return nil, false
		}
		return new(big.Rat).SetInt(new(big.Int).Set(&integer.Int)), true
	case "decimal":
		stringer, ok := value.Interface().(fmt.Stringer)
		if !ok {
			return nil, false
		}
		source = stringer.String()
	case "int32", "int64":
		source = fmt.Sprintf("%d", value.Int())
	case "uint32", "uint64", "size":
		source = fmt.Sprintf("%d", value.Uint())
	case "float32", "float64":
		result := new(big.Rat)
		if result.SetFloat64(value.Float()) == nil {
			return nil, false
		}
		return result, true
	default:
		return nil, false
	}
	result, ok := new(big.Rat).SetString(source)
	return result, ok
}

func contractValueLength(value reflect.Value, typeName string) (int64, bool) {
	if !value.IsValid() {
		return 0, false
	}
	if typeName == "string" {
		if value.Kind() != reflect.String || !utf8.ValidString(value.String()) {
			return 0, false
		}
		return int64(utf8.RuneCountInString(value.String())), true
	}
	switch typeName {
	case "list", "set", "map", "tuple":
		return int64(value.Len()), true
	default:
		return 0, false
	}
}

func validateContractStringFormat(value, format string) error {
	var err error
	switch format {
	case "uuid":
		_, err = ParseUUID(value)
	case "date":
		_, err = ParseDate(value)
	case "datetime":
		_, err = ParseDateTime(value)
	case "duration":
		_, err = decodeJSONDuration(value)
	case "url":
		_, err = ParseURL(value)
	case "relative_path":
		_, err = ParseRelativePath(value)
	default:
		return fmt.Errorf("unsupported contract string format %q", format)
	}
	if err != nil {
		return fmt.Errorf("value does not satisfy %s format: %w", format, err)
	}
	return nil
}
