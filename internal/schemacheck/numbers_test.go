package schemacheck

import "testing"

func TestSchemaNumbersKeepExactConstraints(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		name, schema, payload string
		reject                bool
	}{
		{"unbounded nested number", `{"type":"object"}`, `{"fields":{"n":1e309}}`, false},
		{"large integer", `{"type":"integer"}`, `1e309`, false},
		{"huge exponent", `{"type":"integer"}`, `1e999999999999999999999999999999999999`, false},
		{"huge negative exponent", `{"type":"integer"}`, `1e-999999999999999999999999999999999999`, true},
		{"zero huge exponent", `{"type":"integer"}`, `-0e-999999999999999999999999999999999999`, false},
		{"precise noninteger", `{"type":"integer"}`, `1.000000000000000001`, true},
		{"decimal integer", `{"type":"integer"}`, `100.00e-1`, false},
		{"tiny noninteger", `{"type":"integer"}`, `1e-309`, true},
		{"exact minimum", `{"minimum":9007199254740993}`, `9007199254740992`, true},
		{"exact maximum", `{"maximum":9007199254740992}`, `9007199254740993`, true},
		{"tiny negative minimum", `{"minimum":0}`, `-1e-309`, true},
		{"huge maximum", `{"maximum":1e309}`, `1e310`, true},
		{"negative ordering", `{"minimum":-1e309}`, `-1e310`, true},
		{"equal decimal bound", `{"minimum":1.0,"maximum":1e0}`, `1.00`, false},
		{"exact const", `{"const":9007199254740993}`, `9007199254740992`, true},
		{"equivalent const", `{"const":{"n":[10,null]}}`, `{"n":[1e1,null]}`, false},
		{"different null property", `{"const":{"n":null}}`, `{"other":null}`, true},
		{"equivalent enum", `{"enum":[1.0]}`, `1e0`, false},
		{"different enum", `{"enum":[9007199254740992]}`, `9007199254740993`, true},
		{"equivalent unique numbers", `{"uniqueItems":true}`, `[1,1.0]`, true},
		{"equivalent unique objects", `{"uniqueItems":true}`, `[{"n":10},{"n":1e1}]`, true},
		{"distinct unique integers", `{"uniqueItems":true}`, `[9007199254740992,9007199254740993]`, false},
		{"number versus string", `{"uniqueItems":true}`, `[1,"1"]`, false},
		{"string minimum", `{"minLength":2}`, `"a"`, true},
		{"array maximum", `{"maxItems":1}`, `[0,1]`, true},
		{"object minimum", `{"minProperties":2}`, `{"a":0}`, true},
		{"document suffix", `{}`, `1 2`, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			var schema, value any
			if err := decodeSchemaJSON([]byte(item.schema), &schema); err != nil {
				t.Fatal(err)
			}
			err := decodeSchemaJSON([]byte(item.payload), &value)
			var diagnostics []string
			if err == nil {
				diagnostics = (harnessSchemaValidator{root: schema}).validate(schema, value, "$")
			}
			if (err != nil || len(diagnostics) > 0) != item.reject {
				t.Fatalf("err=%v diagnostics=%v reject=%t", err, diagnostics, item.reject)
			}
		})
	}
}
