package vnext

import (
	"encoding/json"
	"math"
	"testing"
)

func TestMarshalCanonicalSortsKeysAndPreservesUnicode(t *testing.T) {
	b, err := MarshalCanonical(map[string]any{"z": "line\u2028separator", "a": []any{"1", true, nil}})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"a\":[\"1\",true,null],\"z\":\"line separator\"}"
	if string(b) != want {
		t.Fatalf("canonical = %q, want %q", b, want)
	}
}

func TestMarshalCanonicalUsesRFC8785NumberSerialization(t *testing.T) {
	value := []any{
		json.Number("333333333.33333329"), json.Number("1E30"), json.Number("4.50"),
		json.Number("2e-3"), json.Number("1e-27"), json.Number("1e-6"), json.Number("1e-7"),
	}
	encoded, err := MarshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `[333333333.3333333,1e+30,4.5,0.002,1e-27,0.000001,1e-7]`; got != want {
		t.Fatalf("canonical numbers = %s, want %s", got, want)
	}
}

func TestMarshalCanonicalUsesUTF16PropertyOrder(t *testing.T) {
	b, err := MarshalCanonical(map[string]any{"\ue000": 2, "\U00010000": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "{\"𐀀\":1,\"\":2}"; got != want {
		t.Fatalf("canonical = %q, want %q", got, want)
	}
}

func TestMarshalCanonicalRejectsInvalidUTF8AndNormalizesNegativeZero(t *testing.T) {
	if _, err := MarshalCanonical(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	b, err := MarshalCanonical(math.Copysign(0, -1))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "0" {
		t.Fatalf("negative zero = %s", b)
	}
}
