package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
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
		if canonicalUnescapedASCII(typed) {
			output.WriteByte('"')
			output.WriteString(typed)
			output.WriteByte('"')
			return nil
		}
		quotedBytes, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		// Consume JSON escapes as pairs so literal backslashes cannot turn
		// the following u2028/u2029 text into an unescaped separator.
		for index := 0; index < len(quotedBytes); index++ {
			if quotedBytes[index] == '\\' && index+1 < len(quotedBytes) {
				if index+6 <= len(quotedBytes) {
					escape := string(quotedBytes[index : index+6])
					if escape == `\u2028` || escape == `\u2029` {
						output.WriteRune('\u2028' + rune(quotedBytes[index+5]-'8'))
						index += 5
						continue
					}
				}
				output.WriteByte(quotedBytes[index])
				index++
			}
			output.WriteByte(quotedBytes[index])
		}
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
		slices.SortFunc(keys, func(left, right string) int {
			if lessUTF16(left, right) {
				return -1
			}
			if left == right {
				return 0
			}
			return 1
		})
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

func canonicalUnescapedASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character < 0x20 || character >= utf8.RuneSelf {
			return false
		}
		switch character {
		case '"', '\\', '<', '>', '&':
			return false
		}
	}
	return true
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

// lessUTF16 orders strings by UTF-16 code unit, which is what the canonical JSON
// object-key contract requires. Compare the ASCII prefix directly and stop at
// its first difference; a non-ASCII suffix starts at a rune boundary.
func lessUTF16(left, right string) bool {
	for index := range min(len(left), len(right)) {
		a, b := left[index], right[index]
		if a >= utf8.RuneSelf || b >= utf8.RuneSelf {
			return lessNonASCIIUTF16(left[index:], right[index:])
		}
		if a != b {
			return a < b
		}
	}
	return len(left) < len(right)
}

// Supplementary runes retain their relative UTF-16 order. When compared with a
// BMP rune, their leading surrogate determines the order instead.
func lessNonASCIIUTF16(left, right string) bool {
	for left != "" && right != "" {
		a, leftSize := utf8.DecodeRuneInString(left)
		b, rightSize := utf8.DecodeRuneInString(right)
		if a != b {
			if (a <= 0xffff) != (b <= 0xffff) {
				if a > 0xffff {
					a, _ = utf16.EncodeRune(a)
				} else {
					b, _ = utf16.EncodeRune(b)
				}
			}
			return a < b
		}
		left, right = left[leftSize:], right[rightSize:]
	}
	return len(left) < len(right)
}

func validateCanonicalStrings(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	// Compiler/schema values mostly use these JSON containers. Iterating them
	// directly avoids allocating reflection values for every map key and item.
	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case map[string]any:
			for key, item := range typed {
				if !utf8.ValidString(key) {
					return fmt.Errorf("canonical JSON property contains invalid UTF-8")
				}
				if err := validateCanonicalStrings(reflect.ValueOf(item)); err != nil {
					return err
				}
			}
			return nil
		case []any:
			for _, item := range typed {
				if err := validateCanonicalStrings(reflect.ValueOf(item)); err != nil {
					return err
				}
			}
			return nil
		}
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
		entries := value.MapRange()
		for entries.Next() {
			key := entries.Key()
			if key.Kind() == reflect.String && !utf8.ValidString(key.String()) {
				return fmt.Errorf("canonical JSON property contains invalid UTF-8")
			}
			if err := validateCanonicalStrings(entries.Value()); err != nil {
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
