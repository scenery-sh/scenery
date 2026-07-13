package scenery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// DecodeJSONObject is the strict object decoder used by generated contract
// records.
func DecodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	canonical, err := canonicalizeExactJSON(data)
	if err != nil {
		return nil, err
	}
	data = canonical
	if len(data) >= 3 && bytes.Equal(data[:3], []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("JSON byte-order mark is forbidden")
	}
	if !utf8.Valid(data) || !validJSONSurrogateEscapes(data) {
		return nil, fmt.Errorf("JSON contains invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	object := map[string]json.RawMessage{}
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
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		object[key] = append(json.RawMessage(nil), raw...)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("invalid JSON object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, err
	}
	return object, nil
}

func validJSONSurrogateEscapes(data []byte) bool {
	for index := 0; index < len(data); index++ {
		if data[index] != '"' {
			continue
		}
		for index++; index < len(data) && data[index] != '"'; index++ {
			if data[index] != '\\' {
				continue
			}
			index++
			if index >= len(data) {
				return false
			}
			if data[index] != 'u' || index+4 >= len(data) {
				continue
			}
			first, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
			if err != nil {
				return false
			}
			index += 4
			if first >= 0xd800 && first <= 0xdbff {
				if index+6 >= len(data) || data[index+1] != '\\' || data[index+2] != 'u' {
					return false
				}
				second, err := strconv.ParseUint(string(data[index+3:index+7]), 16, 16)
				if err != nil || second < 0xdc00 || second > 0xdfff {
					return false
				}
				index += 6
			} else if first >= 0xdc00 && first <= 0xdfff {
				return false
			}
		}
	}
	return true
}

func (value Nullable[T]) MarshalJSON() ([]byte, error) {
	if value.Null {
		return []byte("null"), nil
	}
	return json.Marshal(value.Value)
}

func (value *Nullable[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		var zero T
		value.Value = zero
		value.Null = true
		return nil
	}
	value.Null = false
	return json.Unmarshal(data, &value.Value)
}

func (value Optional[T]) MarshalJSON() ([]byte, error) {
	if !value.Set {
		return nil, fmt.Errorf("cannot encode an absent optional value directly")
	}
	return json.Marshal(value.Value)
}

func (value *Optional[T]) UnmarshalJSON(data []byte) error {
	value.Set = true
	return json.Unmarshal(data, &value.Value)
}
