package schemacheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
)

func decodeSchemaJSON(data []byte, value any) error {
	if !json.Valid(data) {
		return errors.New("invalid JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(value)
}

type schemaDecimal struct {
	sign   int
	digits string
	order  big.Int
}

// Compare validated JSON decimal tokens without float rounding or expanding
// powers of ten. Even a huge exponent needs only its encoded digits in memory.
func schemaDecimalNumber(number json.Number) schemaDecimal {
	text, sign := number.String(), 1
	if strings.HasPrefix(text, "-") {
		text, sign = text[1:], -1
	}
	var result schemaDecimal
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		result.order.SetString(text[i+1:], 10)
		text = text[:i]
	}
	point := len(text)
	if i := strings.IndexByte(text, '.'); i >= 0 {
		point = i
	}
	digits := strings.ReplaceAll(text, ".", "")
	trimmed := strings.TrimLeft(digits, "0")
	leading := len(digits) - len(trimmed)
	result.digits = strings.TrimRight(trimmed, "0")
	if result.digits == "" {
		result.order.SetInt64(0)
		return result
	}
	result.sign = sign
	result.order.Add(&result.order, big.NewInt(int64(point-leading)))
	return result
}

func compareSchemaNumbers(a, b json.Number) int {
	x, y := schemaDecimalNumber(a), schemaDecimalNumber(b)
	if x.sign != y.sign {
		if x.sign < y.sign {
			return -1
		}
		return 1
	}
	if x.sign == 0 {
		return 0
	}
	if order := x.order.Cmp(&y.order); order != 0 {
		return order * x.sign
	}
	for i := 0; i < max(len(x.digits), len(y.digits)); i++ {
		left, right := byte('0'), byte('0')
		if i < len(x.digits) {
			left = x.digits[i]
		}
		if i < len(y.digits) {
			right = y.digits[i]
		}
		if left < right {
			return -x.sign
		}
		if left > right {
			return x.sign
		}
	}
	return 0
}

func schemaNumberIsInteger(number json.Number) bool {
	value := schemaDecimalNumber(number)
	return value.sign == 0 || value.order.Cmp(big.NewInt(int64(len(value.digits)))) >= 0
}

func schemaValuesEqual(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		other, ok := b.(json.Number)
		return ok && compareSchemaNumbers(a, other) == 0
	case []any:
		other, ok := b.([]any)
		if !ok || len(a) != len(other) {
			return false
		}
		for i := range a {
			if !schemaValuesEqual(a[i], other[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := b.(map[string]any)
		if !ok || len(a) != len(other) {
			return false
		}
		for key, value := range a {
			otherValue, present := other[key]
			if !present || !schemaValuesEqual(value, otherValue) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Unique-item keys retain numeric equality across equivalent token spellings.
func canonicalSchemaNumbers(value any) any {
	switch value := value.(type) {
	case json.Number:
		number := schemaDecimalNumber(value)
		if number.sign == 0 {
			return json.Number("0")
		}
		prefix := ""
		if number.sign < 0 {
			prefix = "-"
		}
		number.order.Sub(&number.order, big.NewInt(int64(len(number.digits))))
		return json.Number(prefix + number.digits + "e" + number.order.String())
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = canonicalSchemaNumbers(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = canonicalSchemaNumbers(item)
		}
		return out
	default:
		return value
	}
}
