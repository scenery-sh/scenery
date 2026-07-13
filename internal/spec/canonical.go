package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func MarshalCanonical(value any) ([]byte, error) {
	if err := validateCanonicalStrings(reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, normalized); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		quotedBytes, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		quoted := string(quotedBytes)
		quoted = string(bytes.ReplaceAll([]byte(quoted), []byte(`\u2028`), []byte(" ")))
		quoted = string(bytes.ReplaceAll([]byte(quoted), []byte(`\u2029`), []byte(" ")))
		output.WriteString(quoted)
	case json.Number:
		canonical, err := canonicalJSONNumber(typed.String())
		if err != nil {
			return err
		}
		output.WriteString(canonical)
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, key); err != nil {
				return err
			}
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

func canonicalJSONNumber(source string) (string, error) {
	value, err := strconv.ParseFloat(source, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid JSON number %q", source)
	}
	if value == 0 {
		return "0", nil
	}
	negative := value < 0
	if negative {
		value = -value
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	separator := strings.LastIndexByte(scientific, 'e')
	if separator < 0 {
		return "", fmt.Errorf("invalid canonical JSON number %q", source)
	}
	mantissa, exponentText := scientific[:separator], scientific[separator+1:]
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return "", fmt.Errorf("invalid canonical JSON number %q", source)
	}
	digits := strings.ReplaceAll(mantissa, ".", "")
	var result string
	if exponent >= -6 && exponent < 21 {
		decimalPosition := 1 + exponent
		switch {
		case decimalPosition <= 0:
			result = "0." + strings.Repeat("0", -decimalPosition) + digits
		case decimalPosition >= len(digits):
			result = digits + strings.Repeat("0", decimalPosition-len(digits))
		default:
			result = digits[:decimalPosition] + "." + digits[decimalPosition:]
		}
	} else {
		result = digits[:1]
		if len(digits) > 1 {
			result += "." + digits[1:]
		}
		if exponent >= 0 {
			result += "e+" + strconv.Itoa(exponent)
		} else {
			result += "e" + strconv.Itoa(exponent)
		}
	}
	if negative {
		result = "-" + result
	}
	return result, nil
}

func lessUTF16(left, right string) bool {
	a := utf16.Encode([]rune(left))
	b := utf16.Encode([]rune(right))
	for index := 0; index < len(a) && index < len(b); index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return len(a) < len(b)
}

func validateCanonicalStrings(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		return validateCanonicalStrings(value.Elem())
	}
	switch value.Kind() {
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return fmt.Errorf("canonical JSON contains invalid UTF-8")
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			if key.Kind() == reflect.String && !utf8.ValidString(key.String()) {
				return fmt.Errorf("canonical JSON property contains invalid UTF-8")
			}
			if err := validateCanonicalStrings(value.MapIndex(key)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := validateCanonicalStrings(value.Index(index)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath == "" {
				if err := validateCanonicalStrings(value.Field(index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
