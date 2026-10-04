package spec

import (
	"encoding/json"
	"testing"
)

func TestMarshalCanonicalStringEscapingAndValidation(t *testing.T) {
	for character := 0; character < 128; character++ {
		value := "left" + string(rune(character)) + "right"
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := MarshalCanonical(value)
		if err != nil || string(got) != string(want) {
			t.Fatalf("ASCII %d: canonical JSON = %q, %v; want %q", character, got, err, want)
		}
	}

	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "ASCII", value: "plain", want: "\"plain\""},
		{name: "separators", value: "a\u2028b\u2029c", want: "\"a\u2028b\u2029c\""},
		{name: "literal separator escapes", value: `\u2028\u2029`, want: `"\\u2028\\u2029"`},
		{name: "backslash before separators", value: "\\\u2028\\\u2029", want: "\"\\\\\u2028\\\\\u2029\""},
		{name: "literal escape key", value: map[string]any{`\u2028`: `\\u2029`}, want: `{"\\u2028":"\\\\u2029"}`},
		{name: "HTML escaping", value: "<>&", want: "\"\\u003c\\u003e\\u0026\""},
		{name: "quotes and controls", value: "\"\\\n\x00", want: "\"\\\"\\\\\\n\\u0000\""},
		{name: "nested map", value: map[string]any{"b": []any{"\u2028"}, "a": "<"}, want: "{\"a\":\"\\u003c\",\"b\":[\"\u2028\"]}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := MarshalCanonical(test.value)
			if err != nil || string(got) != test.want {
				t.Fatalf("canonical JSON = %q, %v; want %q", got, err, test.want)
			}
		})
	}

	invalid := string([]byte{0xff})
	type namedMap map[string]any
	type namedString string
	for name, value := range map[string]any{
		"map key":                        map[string]any{invalid: "value"},
		"map value":                      map[string]any{"key": invalid},
		"nested map":                     []any{map[string]any{"key": []string{invalid}}},
		"struct field":                   struct{ Value string }{invalid},
		"named map in JSON container":    map[string]any{"value": namedMap{"key": invalid}},
		"named string in JSON container": []any{namedString(invalid)},
		"pointer in JSON container":      map[string]any{"value": &invalid},
		"struct in JSON container":       []any{struct{ Value any }{[]any{invalid}}},
	} {
		t.Run(name, func(t *testing.T) {
			if encoded, err := MarshalCanonical(value); err == nil {
				t.Fatalf("invalid UTF-8 accepted: %q", encoded)
			}
		})
	}
}

func TestMarshalCanonicalMixedContainersPreserveJSONNormalization(t *testing.T) {
	type namedMap map[string]any
	var absent *string
	value := map[string]any{
		"binary": []byte{0xff},
		"record": struct {
			Value   any `json:"value"`
			private string
		}{Value: []any{namedMap{"number": int64(7)}, absent}, private: string([]byte{0xff})},
	}
	encoded, err := MarshalCanonical(value)
	want := `{"binary":"/w==","record":{"value":[{"number":7},null]}}`
	if err != nil || string(encoded) != want {
		t.Fatalf("canonical JSON = %q, %v; want %q", encoded, err, want)
	}
}
