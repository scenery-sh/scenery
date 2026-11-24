package contract

import (
	"encoding/json"
	"testing"
)

func TestManifestWireKeepsAbsenceAndNullAcrossSerialization(t *testing.T) {
	types := ManifestTypes{"pricing/record/quote": {
		Kind: "record", UnknownFields: "reject",
		Fields: []ManifestField{{Name: "amount", Type: "int64"}, {Name: "note", Type: "optional(nullable(string))"}},
	}}
	encoded, err := json.Marshal(types)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ManifestTypes
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{"amount":"9007199254740993"}`, `{"amount":"9223372036854775807","note":null}`} {
		got, err := decoded.Normalize("pricing/record/quote", []byte(input))
		if err != nil || string(got) != input {
			t.Fatalf("normalize %s = %s, %v", input, got, err)
		}
	}
	for _, invalid := range []string{`{"amount":9007199254740993}`, `{"amount":"1","amount":"2"}`, `{"amount":"1","unknown":true}`} {
		if _, err := decoded.Normalize("pricing/record/quote", []byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}
