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
	for name, value := range map[string]any{
		"map key":      map[string]any{invalid: "value"},
		"map value":    map[string]any{"key": invalid},
		"nested map":   []any{map[string]any{"key": []string{invalid}}},
		"struct field": struct{ Value string }{invalid},
	} {
		t.Run(name, func(t *testing.T) {
			if encoded, err := MarshalCanonical(value); err == nil {
				t.Fatalf("invalid UTF-8 accepted: %q", encoded)
			}
		})
	}
}
