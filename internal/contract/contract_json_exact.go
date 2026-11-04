package contract

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const maxExactCanonicalJSONExpansionBytes = 16 << 20

func canonicalizeExactJSON(data []byte) ([]byte, error) {
	if isCanonicalExactJSON(data) {
		return data, nil
	}
	return canonicalizeExactJSONFull(data)
}

// canonicalizeExactJSONFull is the defining decode/re-encode canonicalization
// pass. isCanonicalExactJSON may only skip it for inputs this pass would
// return unchanged.
func canonicalizeExactJSONFull(data []byte) ([]byte, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	value, err := decodeExactJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value %v", token)
		}
		return nil, err
	}
	var output bytes.Buffer
	budget := exactCanonicalJSONBudget(len(data))
	if err := writeExactCanonicalJSON(&output, value, budget); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// exactCanonicalJSONBudget bounds growth beyond the already-buffered input.
// Large ordinary documents may remain large, while compact values such as
// exponent-form numbers cannot expand by more than the fixed safety margin.
func exactCanonicalJSONBudget(inputBytes int) int {
	maxInt := int(^uint(0) >> 1)
	ratioBudget := maxInt
	if inputBytes <= (maxInt-1024)/8 {
		ratioBudget = inputBytes*8 + 1024
	}
	expansionBudget := maxInt
	if inputBytes <= maxInt-maxExactCanonicalJSONExpansionBytes {
		expansionBudget = inputBytes + maxExactCanonicalJSONExpansionBytes
	}
	return min(ratioBudget, expansionBudget)
}

func decodeStrictContractJSON(data []byte, target any) error {
	if _, err := canonicalizeExactJSON(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
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

// Keep numeric token text intact; the canonical writer normalizes it without
// rounding through float64. jsontext owns syntax, Unicode and duplicate checks.
func decodeExactJSONValue(decoder *jsontext.Decoder) (any, error) {
	token, err := decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	switch token.Kind() {
	case 'n':
		return nil, nil
	case 't', 'f':
		return token.Bool(), nil
	case '"':
		return token.String(), nil
	case '0':
		return json.Number(token.String()), nil
	case '{':
		object := map[string]any{}
		for decoder.PeekKind() != '}' {
			keyToken, err := decoder.ReadToken()
			if err != nil {
				return nil, err
			}
			key := keyToken.String()
			value, err := decodeExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		values := []any{}
		for decoder.PeekKind() != ']' {
			value, err := decodeExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected JSON token %v", token)
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
		if exactJSONStringNeedsNoEscapes(typed) {
			if err := write(`"`); err != nil {
				return err
			}
			if err := write(typed); err != nil {
				return err
			}
			return write(`"`)
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		// Consume escapes as pairs. A literal backslash followed by u2028
		// or u2029 is string content, not an encoded Unicode separator.
		start := 0
		for index := 0; index < len(encoded); index++ {
			if encoded[index] != '\\' {
				continue
			}
			if index+6 <= len(encoded) {
				escape := string(encoded[index : index+6])
				if escape == `\u2028` || escape == `\u2029` {
					if err := write(string(encoded[start:index])); err != nil {
						return err
					}
					separator := "\u2028"
					if encoded[index+5] == '9' {
						separator = "\u2029"
					}
					if err := write(separator); err != nil {
						return err
					}
					index += 5
					start = index + 1
					continue
				}
			}
			index++
		}
		return write(string(encoded[start:]))
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

// Values reach the writer only after strict UTF-8 decoding. Non-ASCII scalar
// values, including U+2028/U+2029, can therefore be copied directly.
func exactJSONStringNeedsNoEscapes(value string) bool {
	for index := range len(value) {
		character := value[index]
		if character < 0x20 || character == '"' || character == '\\' || character == '<' || character == '>' || character == '&' {
			return false
		}
	}
	return true
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

// contractUTF16Less orders object keys by UTF-16 code unit without allocating
// encoded copies. ASCII keys can use the ordinary byte comparison.
func contractUTF16Less(left, right string) bool {
	if contractASCIIOnly(left) && contractASCIIOnly(right) {
		return left < right
	}
	for len(left) > 0 && len(right) > 0 {
		a, aSize := utf8.DecodeRuneInString(left)
		b, bSize := utf8.DecodeRuneInString(right)
		if a != b {
			// Scalar order also orders two surrogate pairs. When only one
			// scalar needs a pair, compare its leading unit with the BMP scalar.
			if (a >= 0x10000) != (b >= 0x10000) {
				if a >= 0x10000 {
					a, _ = utf16.EncodeRune(a)
				} else {
					b, _ = utf16.EncodeRune(b)
				}
			}
			return a < b
		}
		left, right = left[aSize:], right[bSize:]
	}
	return len(left) < len(right)
}

func contractASCIIOnly(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= 0x80 {
			return false
		}
	}
	return true
}
