package compiler

import (
	"strings"
	"testing"

	"scenery.sh/internal/scn"
)

func TestConfigWireJSONWritesCanonicalNumbersAndPlainJSON(t *testing.T) {
	t.Parallel()
	exact := scn.ExactNumericScalar
	for _, test := range []struct {
		value any
		typ   string
		want  string
	}{
		{exact("1000000"), "float64", "1e+06"},
		{exact("2500000"), "optional(float64)", "2.5e+06"},
		{exact("0.00001"), "float64", "1e-05"},
		{exact("1.5"), "float64", "1.5"},
		{exact("0.1"), "float32", "0.1"},
		{exact("3000000000"), "uint32", "3000000000"},
		{exact("9007199254740993"), "int64", `"9007199254740993"`},
		{exact("7"), "int", `"7"`},
		{exact("5"), "json", "5"},
		{map[string]any{"retries": exact("3"), "ratio": exact("0.25"), "tags": []any{"a", exact("-12")}}, "json", `{"ratio":0.25,"retries":3,"tags":["a",-12]}`},
	} {
		got, err := ConfigWireJSON(test.value, test.typ)
		if err != nil || string(got) != test.want {
			t.Errorf("%v as %s = %s, %v; want %s", test.value, test.typ, got, err, test.want)
		}
	}
	for _, test := range []struct {
		value any
		typ   string
	}{
		{exact("3000000000"), "int32"},
		{exact("-1"), "uint32"},
		{exact("1.5"), "int64"},
		{exact("1" + strings.Repeat("0", 39)), "float32"},
		{map[string]any{"owner": map[string]any{"$ref": "service.billing"}}, "json"},
	} {
		if got, err := ConfigWireJSON(test.value, test.typ); err == nil {
			t.Errorf("%v as %s = %s, want an error", test.value, test.typ, got)
		}
	}
}

func TestStaticWireJSONUnwrapsNestedJSONDefaults(t *testing.T) {
	t.Parallel()
	record := Resource{Address: "billing/record/plan", Module: "billing", Kind: "scenery.record"}
	field := map[string]any{"name": "options", "type": "json", "default": map[string]any{
		"limits": map[string]any{"retries": scn.ExactNumericScalar("3"), "ratio": scn.ExactNumericScalar("0.25")},
	}}
	got, err := StaticFieldDefaultJSON(nil, record, field)
	if err != nil || string(got) != `{"limits":{"ratio":0.25,"retries":3}}` {
		t.Fatalf("json default = %s, %v", got, err)
	}
	field = map[string]any{"name": "threshold", "type": "float64", "default": scn.ExactNumericScalar("1000000")}
	if got, err = StaticFieldDefaultJSON(nil, record, field); err != nil || string(got) != "1e+06" {
		t.Fatalf("float default = %s, %v", got, err)
	}
}
