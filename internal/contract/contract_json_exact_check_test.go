package contract

import (
	"bytes"
	"strings"
	"testing"
)

// canonicalExactJSONFixtures are representative valid documents covering the
// wire shapes generated contract packages emit: nested records, lists of
// records, quoted int64 values, escaped and raw Unicode, HTML-significant
// characters, and canonical number spellings.
var canonicalExactJSONFixtures = []string{
	`null`, `true`, `false`, `0`, `-1`, `42`, `0.5`, `-0.5`, `123.456`,
	`1000000000000000000000`, `""`, `"plain"`, `"quote \" backslash \\"`,
	"\"tab \\t newline \\n return \\r backspace \\b formfeed \\f\"",
	`"html \u003c\u003e\u0026"`,
	`"control \u0000\u0001\u000b\u001f"`, `"háček 😀 ✓"`, "\"raw fffd \ufffd\"",
	`{}`, `[]`, `[[]]`, `[{}]`, `["a","b","a"]`, `[1,2,3]`,
	`{"a":1,"b":2,"z":[true,null]}`,
	`{"":"empty key","a":null}`,
	`{"count":"9223372036854775807","items":[{"id":"one","tags":["x","y"]},{"id":"two","tags":[]}],"total":"2"}`,
	`{"kind":"result","name":"ok","value":{"nested":{"deep":{"deeper":[{"leaf":"value"}]}}}}`,
	`{"ahj_id":"CA-001","latitude":37.7749,"longitude":-122.4194,"name":"San Francisco","state":"California"}`,
}

// nonCanonicalExactJSONFixtures pair inputs the fast path must reject with the
// canonical form the full pass produces for them.
var nonCanonicalExactJSONFixtures = []struct{ input, canonical string }{
	{` 1 `, `1`},
	{`[1, 2]`, `[1,2]`},
	{"{\n\t\"a\": 1\n}", `{"a":1}`},
	{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
	{`1e2`, `100`},
	{`1E+2`, `100`},
	{`1.50`, `1.5`},
	{`-0`, `0`},
	{`-0.0`, `0`},
	{`0.10`, `0.1`},
	{`10e-1`, `1`},
	{`"\u0041"`, `"A"`},
	{`"\/"`, `"/"`},
	{`"<&>"`, `"\u003c\u0026\u003e"`},
	{`"\u003C"`, `"\u003c"`},
	{`"\ud83d\ude00"`, `"😀"`},
	{`"\u2028"`, "\"\u2028\""},
	{`"\u0008\u000c"`, `"\b\f"`},
	{`"\ufffd"`, "\"\ufffd\""},
	{`{"š":1,"a":2}`, `{"a":2,"š":1}`},
	{`{"a\u0062":1}`, `{"ab":1}`},
}

var invalidExactJSONFixtures = []string{
	``, `{`, `[1,]`, `{"a":1,"a":2}`, `1 2`, `nul`, `"unterminated`,
	"\xef\xbb\xbf{}", "\"invalid \xff utf8\"", `"\ud800"`, `01`, `--1`, `+1`,
	`.5`, `1.`, `"bad \x escape"`,
}

func TestIsCanonicalExactJSONAcceptsOnlyFullPassFixedPoints(t *testing.T) {
	inputs := make([]string, 0, len(canonicalExactJSONFixtures)+2*len(nonCanonicalExactJSONFixtures))
	inputs = append(inputs, canonicalExactJSONFixtures...)
	for _, fixture := range nonCanonicalExactJSONFixtures {
		inputs = append(inputs, fixture.input, fixture.canonical)
	}
	for _, input := range inputs {
		if !isCanonicalExactJSON([]byte(input)) {
			continue
		}
		full, err := canonicalizeExactJSONFull([]byte(input))
		if err != nil {
			t.Fatalf("fast path accepted %q but the full pass rejects it: %v", input, err)
		}
		if !bytes.Equal(full, []byte(input)) {
			t.Fatalf("fast path accepted %q but the full pass rewrites it to %q", input, full)
		}
	}
}

func TestCanonicalExactJSONFastPathMatchesFullPass(t *testing.T) {
	for _, input := range canonicalExactJSONFixtures {
		if !isCanonicalExactJSON([]byte(input)) {
			t.Fatalf("fast path rejected canonical fixture %q", input)
		}
		combined, err := canonicalizeExactJSON([]byte(input))
		if err != nil {
			t.Fatalf("canonicalize %q: %v", input, err)
		}
		if string(combined) != input {
			t.Fatalf("canonicalize %q = %q, want unchanged", input, combined)
		}
	}
}

func TestCanonicalExactJSONNonCanonicalInputsFallBack(t *testing.T) {
	for _, fixture := range nonCanonicalExactJSONFixtures {
		if isCanonicalExactJSON([]byte(fixture.input)) {
			t.Fatalf("fast path accepted non-canonical %q", fixture.input)
		}
		canonical, err := canonicalizeExactJSON([]byte(fixture.input))
		if err != nil {
			t.Fatalf("canonicalize %q: %v", fixture.input, err)
		}
		if string(canonical) != fixture.canonical {
			t.Fatalf("canonicalize %q = %q, want %q", fixture.input, canonical, fixture.canonical)
		}
		again, err := canonicalizeExactJSON(canonical)
		if err != nil {
			t.Fatalf("re-canonicalize %q: %v", canonical, err)
		}
		if !bytes.Equal(again, canonical) {
			t.Fatalf("canonicalization of %q is not idempotent: %q", fixture.input, again)
		}
	}
}

func TestCanonicalExactJSONInvalidInputsStayRejected(t *testing.T) {
	for _, input := range invalidExactJSONFixtures {
		if isCanonicalExactJSON([]byte(input)) {
			t.Fatalf("fast path accepted invalid input %q", input)
		}
		if _, err := canonicalizeExactJSON([]byte(input)); err == nil {
			t.Fatalf("canonicalize accepted invalid input %q", input)
		}
	}
}

func TestCanonicalExactJSONDeepNestingFallsBack(t *testing.T) {
	deep := strings.Repeat("[", maxCanonicalExactJSONCheckDepth+2) + strings.Repeat("]", maxCanonicalExactJSONCheckDepth+2)
	if isCanonicalExactJSON([]byte(deep)) {
		t.Fatal("fast path accepted a document beyond the depth limit")
	}
	if _, err := canonicalizeExactJSON([]byte(deep)); err == nil {
		t.Fatal("canonicalize accepted a document beyond the decoder depth limit")
	}
}

// FuzzCanonicalExactJSONFastPath proves the fast path only accepts inputs the
// full canonicalization pass would return unchanged, so wire bytes stay
// defined by the full pass.
func FuzzCanonicalExactJSONFastPath(f *testing.F) {
	for _, fixture := range canonicalExactJSONFixtures {
		f.Add([]byte(fixture))
	}
	for _, fixture := range nonCanonicalExactJSONFixtures {
		f.Add([]byte(fixture.input))
		f.Add([]byte(fixture.canonical))
	}
	for _, fixture := range invalidExactJSONFixtures {
		f.Add([]byte(fixture))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !isCanonicalExactJSON(data) {
			return
		}
		full, err := canonicalizeExactJSONFull(data)
		if err != nil {
			t.Fatalf("fast path accepted %q but the full pass rejects it: %v", data, err)
		}
		if !bytes.Equal(full, data) {
			t.Fatalf("fast path accepted %q but the full pass rewrites it to %q", data, full)
		}
	})
}

func BenchmarkCanonicalizeExactJSONFastPath(b *testing.B) {
	document := benchmarkCanonicalDocument(b)
	b.SetBytes(int64(len(document)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := canonicalizeExactJSON(document); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalizeExactJSONFullPass(b *testing.B) {
	document := benchmarkCanonicalDocument(b)
	b.SetBytes(int64(len(document)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := canonicalizeExactJSONFull(document); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkCanonicalDocument(tb testing.TB) []byte {
	var builder strings.Builder
	builder.WriteByte('[')
	for index := range 100 {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`{"county":"Alameda","id":"US-CA-` + strings.Repeat("0", 3) + `","latitude":37.7749,"longitude":-122.4194,"name":"Authority Having Jurisdiction ` + strings.Repeat("x", 32) + `","state":"California","website":"https://example.gov/permits"}`)
	}
	builder.WriteByte(']')
	document := []byte(builder.String())
	if !isCanonicalExactJSON(document) {
		tb.Fatal("benchmark document is not canonical")
	}
	return document
}
