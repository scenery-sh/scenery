package contract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestContractJSONPreservesLiteralSeparatorEscapes(t *testing.T) {
	for _, value := range []string{`\u2028`, `\u2029`, `\\u2028`, "\\\u2028", "\u2028\\u2029", `pattern:\u2028|\u2029`, `"\u2028"`} {
		original, err := json.Marshal(map[string]string{value: value})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := MarshalContractValue(JSON(original), "json")
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]string
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("canonicalization of %q emitted invalid JSON %q: %v", value, encoded, err)
		}
		if len(decoded) != 1 || decoded[value] != value {
			t.Fatalf("canonicalization changed key or value %q into %#v", value, decoded)
		}
		var roundTrip JSON
		if err := UnmarshalContractValue(encoded, &roundTrip, "json"); err != nil || !bytes.Equal(roundTrip, encoded) {
			t.Fatalf("canonical JSON is not stable: %q, %v", roundTrip, err)
		}
	}
}

func FuzzCanonicalJSONStringRoundTrip(f *testing.F) {
	for _, value := range []string{"", "ordinary", "\x00\n\t\r\b\f", "háček 😀", "<>&/", "\u2028\u2029", `\u2028\u2029`, "\\\u2028", `\\\u2029`} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if !utf8.ValidString(value) {
			return
		}
		original, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := canonicalizeExactJSONFull(original)
		if err != nil {
			t.Fatal(err)
		}
		var decoded string
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
			t.Fatalf("string changed: %q -> %q -> %q (%v)", value, encoded, decoded, err)
		}
		if !isCanonicalExactJSON(encoded) {
			t.Fatalf("canonical string was not recognized: %q", encoded)
		}
	})
}

func BenchmarkCanonicalJSONStrings(b *testing.B) {
	for _, value := range []struct{ name, text string }{
		{"ascii", "ordinary string value"},
		{"unicode", "háček 😀 \u2028 \u2029"},
		{"escaped", "line\n<html>&\""},
		{"literal-escape", `\u2028\u2029`},
	} {
		b.Run(value.name, func(b *testing.B) {
			encoded, err := json.Marshal(strings.Repeat(value.text, 32))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := canonicalizeExactJSONFull(encoded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
