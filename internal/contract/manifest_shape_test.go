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
	types := ManifestTypes{"quote/record/Limits": {Kind: "record", Fields: []ManifestField{
		{Name: "limits", Type: "map(int32)", Constraints: ContractConstraints{MinItems: new(int64(1)), MaxItems: new(int64(2))}},
		{Name: "tags", Type: "optional(list(string))", Constraints: ContractConstraints{MinLength: new(int64(1)), MaxLength: new(int64(2))}},
	}}}
	for _, test := range []struct {
		input string
		valid bool
	}{
		{`{"limits":{}}`, false},
		{`{"limits":{"a":1}}`, true},
		{`{"limits":{"a":1,"b":2}}`, true},
		{`{"limits":{"a":1,"b":2},"tags":[]}`, false},
		{`{"limits":{"a":1,"b":2},"tags":["one","two"]}`, true},
		{`{"limits":{"a":1,"b":2},"tags":["one","two","three"]}`, false},
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

func TestManifestNormalizesSizeLikeNativeContracts(t *testing.T) {
	types := ManifestTypes{"files/record/Upload": {Kind: "record", Fields: []ManifestField{
		{Name: "limit", Type: "size", Constraints: ContractConstraints{Minimum: new("1"), Maximum: new("18446744073709551616")}},
		{Name: "quota", Type: "optional(size)"},
	}}}
	for _, test := range []struct {
		input string
		valid bool
	}{
		{`{"limit":"1024"}`, true},
		// A size stays exact beyond the machine integer range.
		{`{"limit":"18446744073709551616","quota":"0"}`, true},
		{`{"limit":1024}`, false},
		{`{"limit":"1KiB"}`, false},
		{`{"limit":"01"}`, false},
		{`{"limit":"-1"}`, false},
		{`{"limit":"0"}`, false},
		{`{"limit":"18446744073709551617"}`, false},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := types.Normalize("files/record/Upload", []byte(test.input))
			if test.valid && (err != nil || string(got) != test.input) {
				t.Fatalf("normalize = %s, %v", got, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("accepted %s as %s", test.input, got)
			}
		})
	}
	// The manifest codec accepts exactly what the native codec accepts.
	for _, raw := range []string{`"1024"`, `1024`, `"1KiB"`} {
		var native Size
		nativeErr := UnmarshalContractValue([]byte(raw), &native, "size")
		_, manifestErr := types.Normalize("size", []byte(raw))
		if (nativeErr == nil) != (manifestErr == nil) {
			t.Fatalf("size %s: native %v, manifest %v", raw, nativeErr, manifestErr)
		}
	}
}
