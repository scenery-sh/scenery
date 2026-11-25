package contract

import (
	"encoding/json"
	"strings"
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

func TestManifestMapItemCount(t *testing.T) {
	types := ManifestTypes{"quote/record/Limits": {Kind: "record", Fields: []ManifestField{{
		Name: "limits", Type: "map(int32)", Constraints: ContractConstraints{MinItems: new(int64(1)), MaxItems: new(int64(2))},
	}}}}
	for _, test := range []struct {
		input string
		valid bool
	}{
		{`{"limits":{}}`, false},
		{`{"limits":{"a":1}}`, true},
		{`{"limits":{"a":1,"b":2}}`, true},
		{`{"limits":{"a":1,"b":2,"c":"bad"}}`, false},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := types.Normalize("quote/record/Limits", []byte(test.input))
			if test.valid {
				if err != nil || string(got) != test.input {
					t.Fatalf("normalize = %s, %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "item count limits") {
				t.Fatalf("expected an item count error, got %v", err)
			}
		})
	}
}
