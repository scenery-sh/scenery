package scenery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const maxExactCanonicalJSONBytes = 16 << 20

func canonicalizeExactJSON(data []byte) ([]byte, error) {
	if len(data) >= 3 && bytes.Equal(data[:3], []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("JSON byte-order mark is forbidden")
	}
	if !utf8.Valid(data) || !validJSONSurrogateEscapes(data) {
		return nil, fmt.Errorf("JSON contains invalid Unicode")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeExactJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value %v", token)
		}
		return nil, err
	}
	var output bytes.Buffer
	budget := len(data)*8 + 1024
	if budget > maxExactCanonicalJSONBytes {
		budget = maxExactCanonicalJSONBytes
	}
	if err := writeExactCanonicalJSON(&output, value, budget); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeStrictContractJSON(data []byte, target any) error {
	canonical, err := canonicalizeExactJSON(data)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func decodeExactJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON object member %q", key)
			}
			value, err := decodeExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim('}') {
			return nil, fmt.Errorf("invalid JSON object")
		}
		return object, nil
	case '[':
		values := []any{}
		for decoder.More() {
			value, err := decodeExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim(']') {
			return nil, fmt.Errorf("invalid JSON array")
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func writeExactCanonicalJSON(output *bytes.Buffer, value any, budget int) error {
	write := func(value string) error {
		if len(value) > budget-output.Len() {
			return fmt.Errorf("canonical JSON exceeds %d-byte expansion budget", budget)
		}
		output.WriteString(value)
		return nil
	}
	switch typed := value.(type) {
	case nil:
		return write("null")
	case bool:
		if typed {
			return write("true")
		} else {
			return write("false")
		}
	case string:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte(" "))
		encoded = bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte(" "))
		return write(string(encoded))
	case json.Number:
		normalized, err := normalizeExactJSONNumber(typed.String(), budget-output.Len())
		if err != nil {
			return err
		}
		return write(normalized)
	case []any:
		if err := write("["); err != nil {
			return err
		}
		for index, item := range typed {
			if index > 0 {
				if err := write(","); err != nil {
					return err
				}
			}
			if err := writeExactCanonicalJSON(output, item, budget); err != nil {
				return err
			}
		}
		return write("]")
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return contractUTF16Less(keys[i], keys[j]) })
		if err := write("{"); err != nil {
			return err
		}
		for index, key := range keys {
			if index > 0 {
				if err := write(","); err != nil {
					return err
				}
			}
			if err := writeExactCanonicalJSON(output, key, budget); err != nil {
				return err
			}
			if err := write(":"); err != nil {
				return err
			}
			if err := writeExactCanonicalJSON(output, typed[key], budget); err != nil {
				return err
			}
		}
		return write("}")
	default:
		return fmt.Errorf("unsupported exact JSON value %T", value)
	}
}

func normalizeExactJSONNumber(source string, budget int) (string, error) {
	if source == "" {
		return "", fmt.Errorf("empty JSON number")
	}
	negative := strings.HasPrefix(source, "-")
	unsigned := strings.TrimPrefix(source, "-")
	exponent := int64(0)
	if index := strings.IndexAny(unsigned, "eE"); index >= 0 {
		parsed, err := strconv.ParseInt(unsigned[index+1:], 10, 32)
		if err != nil || parsed < -1_000_000 || parsed > 1_000_000 {
			return "", fmt.Errorf("JSON number exponent is out of range")
		}
		exponent = parsed
		unsigned = unsigned[:index]
	}
	fractionLength := int64(0)
	if index := strings.IndexByte(unsigned, '.'); index >= 0 {
		fractionLength = int64(len(unsigned) - index - 1)
		unsigned = unsigned[:index] + unsigned[index+1:]
	}
	if unsigned == "" {
		return "", fmt.Errorf("invalid JSON number %q", source)
	}
	digits := strings.TrimLeft(unsigned, "0")
	if digits == "" {
		return "0", nil
	}
	scale := fractionLength - exponent
	if scale < 0 {
		if -scale > int64(budget-len(digits)) {
			return "", fmt.Errorf("canonical JSON number exceeds expansion budget")
		}
		digits += strings.Repeat("0", int(-scale))
		scale = 0
	}
	for scale > 0 && strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale--
	}
	prefix := ""
	if negative {
		prefix = "-"
	}
	if scale == 0 {
		return prefix + digits, nil
	}
	if scale >= int64(len(digits)) {
		if scale+2 > int64(budget) {
			return "", fmt.Errorf("canonical JSON number exceeds expansion budget")
		}
		return prefix + "0." + strings.Repeat("0", int(scale)-len(digits)) + digits, nil
	}
	index := len(digits) - int(scale)
	return prefix + digits[:index] + "." + digits[index:], nil
}

func joinContractJSONObject(members map[string][]byte) ([]byte, error) {
	keys := make([]string, 0, len(members))
	for key := range members {
		if !utf8.ValidString(key) {
			return nil, fmt.Errorf("JSON object key contains invalid UTF-8")
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return contractUTF16Less(keys[i], keys[j]) })
	var output bytes.Buffer
	output.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			output.WriteByte(',')
		}
		encodedKey, _ := json.Marshal(key)
		output.Write(encodedKey)
		output.WriteByte(':')
		output.Write(members[key])
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func contractUTF16Less(left, right string) bool {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for index := 0; index < len(a) && index < len(b); index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return len(a) < len(b)
}
