package scenery

import (
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func contractValidationValue(value any, typeExpression string) (any, error) {
	typeValue, err := parseContractWireType(typeExpression)
	if err != nil {
		return nil, err
	}
	return contractValidationReflectValue(reflect.ValueOf(value), typeValue)
}

func contractValidationReflectValue(value reflect.Value, typeValue contractWireType) (any, error) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, nil
	}
	if typeValue.name == "optional" {
		set := value.FieldByName("Set")
		if !set.IsValid() || !set.Bool() {
			return nil, nil
		}
		return contractValidationReflectValue(value.FieldByName("Value"), typeValue.args[0])
	}
	if typeValue.name == "nullable" {
		null := value.FieldByName("Null")
		if !null.IsValid() || null.Bool() {
			return nil, nil
		}
		return contractValidationReflectValue(value.FieldByName("Value"), typeValue.args[0])
	}
	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case Int:
			return new(big.Rat).SetInt(new(big.Int).Set(&typed.Int)), nil
		case Decimal:
			return contractValidationNumber(typed.String())
		case DateTime:
			return contractValidationDateTime(time.Time(typed)), nil
		case Duration:
			return new(big.Rat).SetInt64(int64(typed)), nil
		case Date:
			parsed, err := time.Parse("2006-01-02", string(typed))
			if err != nil {
				return nil, err
			}
			return new(big.Rat).SetInt64(contractValidationCivilDays(parsed.Year(), int(parsed.Month()), parsed.Day())), nil
		}
	}
	switch typeValue.name {
	case "bool":
		return value.Bool(), nil
	case "int", "int32", "int64", "duration":
		return new(big.Rat).SetInt64(value.Int()), nil
	case "uint32", "uint64", "size":
		integer := new(big.Int).SetUint64(value.Uint())
		return new(big.Rat).SetInt(integer), nil
	case "float32":
		return contractValidationNumber(strconv.FormatFloat(value.Float(), 'g', -1, 32))
	case "float64":
		return contractValidationNumber(strconv.FormatFloat(value.Float(), 'g', -1, 64))
	case "decimal":
		return contractValidationNumber(fmt.Sprint(value.Interface()))
	case "string", "uuid", "url", "relative_path":
		return fmt.Sprint(value.Interface()), nil
	case "bytes":
		if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
			return string(value.Bytes()), nil
		}
		return fmt.Sprint(value.Interface()), nil
	case "date":
		parsed, err := time.Parse("2006-01-02", fmt.Sprint(value.Interface()))
		if err != nil {
			return nil, err
		}
		return new(big.Rat).SetInt64(contractValidationCivilDays(parsed.Year(), int(parsed.Month()), parsed.Day())), nil
	case "datetime":
		parsed, err := time.Parse(time.RFC3339Nano, fmt.Sprint(value.Interface()))
		if err != nil {
			return nil, err
		}
		return contractValidationDateTime(parsed), nil
	case "list", "set":
		items := make([]any, value.Len())
		for index := range items {
			item, err := contractValidationReflectValue(value.Index(index), typeValue.args[0])
			if err != nil {
				return nil, err
			}
			items[index] = item
		}
		return items, nil
	case "map":
		items := map[string]any{}
		iterator := value.MapRange()
		for iterator.Next() {
			key, item := iterator.Key(), iterator.Value()
			converted, err := contractValidationReflectValue(item, typeValue.args[0])
			if err != nil {
				return nil, err
			}
			items[key.String()] = converted
		}
		return items, nil
	case "tuple":
		items := make([]any, len(typeValue.args))
		for index := range items {
			item, err := contractValidationReflectValue(value.FieldByName("Item"+strconv.Itoa(index)), typeValue.args[index])
			if err != nil {
				return nil, err
			}
			items[index] = item
		}
		return items, nil
	default:
		if strings.HasPrefix(typeValue.name, "enum.") && value.Kind() == reflect.String {
			return value.String(), nil
		}
		return contractValidationGenericValue(value.Interface())
	}
}

func contractValidationDateTime(value time.Time) *big.Rat {
	value = value.UTC()
	seconds := big.NewInt(contractValidationCivilDays(value.Year(), int(value.Month()), value.Day()))
	seconds.Mul(seconds, big.NewInt(86_400))
	seconds.Add(seconds, big.NewInt(int64(value.Hour()*3_600+value.Minute()*60+value.Second())))
	nanoseconds := new(big.Int).Mul(seconds, big.NewInt(1_000_000_000))
	nanoseconds.Add(nanoseconds, big.NewInt(int64(value.Nanosecond())))
	return new(big.Rat).SetInt(nanoseconds)
}

func contractValidationCivilDays(year, month, day int) int64 {
	adjustedYear := year
	if month <= 2 {
		adjustedYear--
	}
	era := contractValidationFloorDiv(adjustedYear, 400)
	yearOfEra := adjustedYear - era*400
	monthPrime := month + 9
	if month > 2 {
		monthPrime = month - 3
	}
	dayOfYear := (153*monthPrime+2)/5 + day - 1
	dayOfEra := yearOfEra*365 + yearOfEra/4 - yearOfEra/100 + dayOfYear
	return int64(era*146_097 + dayOfEra - 719_468)
}

func contractValidationFloorDiv(value, divisor int) int {
	quotient, remainder := value/divisor, value%divisor
	if remainder != 0 && (remainder < 0) != (divisor < 0) {
		quotient--
	}
	return quotient
}
