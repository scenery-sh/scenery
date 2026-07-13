package scenery

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestContractWireValueUsesSchemaDirectedExactEncodings(t *testing.T) {
	encoded, err := MarshalContractValue(Set[int64]{10, 2}, "set(int64)")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `["10","2"]`; got != want {
		t.Fatalf("encoded = %s, want %s", got, want)
	}

	var decoded Set[int64]
	if err := UnmarshalContractValue(encoded, &decoded, "set(int64)"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, Set[int64]{10, 2}) {
		t.Fatalf("decoded = %#v", decoded)
	}

	if _, err := MarshalContractValue(Set[int64]{2, 2}, "set(int64)"); err == nil {
		t.Fatal("duplicate set element was accepted")
	}
	if err := UnmarshalContractValue([]byte(`["2","2"]`), &decoded, "set(int64)"); err == nil {
		t.Fatal("duplicate encoded set element was accepted")
	}
	if _, err := MarshalContractValue(math.Copysign(0, -1), "float64"); err == nil {
		t.Fatal("negative zero was accepted")
	}
}

func TestContractWireJSONRejectsAmbiguityAndCanonicalizes(t *testing.T) {
	if _, err := MarshalContractValue(JSON(`{"a":1,"a":2}`), "json"); err == nil {
		t.Fatal("duplicate JSON member was accepted")
	}
	encoded, err := MarshalContractValue(JSON(`{"z":9007199254740993,"a":1.2300}`), "json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"a":1.23,"z":9007199254740993}`; got != want {
		t.Fatalf("canonical JSON = %s, want %s", got, want)
	}
	var decoded JSON
	if err := UnmarshalContractValue(encoded, &decoded, "json"); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(decoded) || string(decoded) != string(encoded) {
		t.Fatalf("decoded JSON = %s", decoded)
	}
}

func TestContractConstraintsValidateExactValues(t *testing.T) {
	minimum, maximum := "2", "10"
	minLength, maxLength := int64(2), int64(4)
	if err := ValidateContractValue(int64(9), "int64", ContractConstraints{Minimum: &minimum, Maximum: &maximum}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateContractValue(int64(11), "int64", ContractConstraints{Maximum: &maximum}); err == nil {
		t.Fatal("maximum was not enforced")
	}
	if err := ValidateContractValue("éx", "string", ContractConstraints{MinLength: &minLength, MaxLength: &maxLength, Pattern: `^é`}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateContractValue("x", "string", ContractConstraints{MinLength: &minLength}); err == nil {
		t.Fatal("Unicode scalar minimum length was not enforced")
	}
}

func TestContractOutcomeEnvelopeIsCanonicalAndStrict(t *testing.T) {
	encoded, err := MarshalContractOutcomeVariant("result", "ok", int64(9007199254740993), "int64")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"result","name":"ok","value":"9007199254740993"}`
	if string(encoded) != want {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}
	kind, name, payload, err := DecodeContractOutcomeEnvelope(encoded)
	if err != nil || kind != "result" || name != "ok" || string(payload) != `"9007199254740993"` {
		t.Fatalf("decoded = %q %q %s, %v", kind, name, payload, err)
	}
	for _, invalid := range []string{
		`{"kind":"result","kind":"error","name":"ok","value":1}`,
		`{"kind":"result","name":"ok","problem":{},"value":1}`,
		`{"kind":"other","name":"ok","value":1}`,
	} {
		if _, _, _, err := DecodeContractOutcomeEnvelope([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid outcome envelope %s", invalid)
		}
	}
}

func TestContractCompositeKeysPreserveTypeAndPresenceBoundaries(t *testing.T) {
	values := []struct {
		value          any
		typeExpression string
	}{
		{NoneOf[string](), "optional(string)"},
		{Some(""), "optional(string)"},
		{NullOf[string](), "nullable(string)"},
		{ValueOf(""), "nullable(string)"},
		{"0", "string"},
		{int64(0), "int64"},
	}
	seen := map[string]bool{}
	for _, item := range values {
		component, err := EncodeContractKeyComponent(item.value, item.typeExpression)
		if err != nil {
			t.Fatalf("%s: %v", item.typeExpression, err)
		}
		key, err := EncodeContractCompositeKey(component)
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatalf("key collision for %#v as %s: %s", item.value, item.typeExpression, key)
		}
		seen[key] = true
		again, err := EncodeContractCompositeKey(component)
		if err != nil || again != key {
			t.Fatalf("non-deterministic key: %q %q %v", key, again, err)
		}
	}
	if _, err := EncodeContractCompositeKey([]byte(`{"state":"value"}`)); err == nil {
		t.Fatal("non-component JSON was accepted")
	}
}

func TestContractUnitUsesExactlyOneEmptyObjectRepresentation(t *testing.T) {
	encoded, err := MarshalContractValue(Unit{}, "std.type.unit")
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("encoded unit = %s, %v", encoded, err)
	}
	var decoded Unit
	if err := UnmarshalContractValue([]byte("{}"), &decoded, "std.type.unit"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"null", `{"extra":true}`, "[]"} {
		if err := UnmarshalContractValue([]byte(invalid), &decoded, "std.type.unit"); err == nil {
			t.Fatalf("accepted invalid unit %s", invalid)
		}
	}
}
