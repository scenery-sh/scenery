package scenery

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestContractExactScalars(t *testing.T) {
	integer, err := ParseInt("-123456789012345678901234567890")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, integer, `"-123456789012345678901234567890"`)
	decimal, err := ParseDecimal("001.2300")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, decimal, `"1.23"`)
	if _, err := ParseUUID("018F47A2-6F45-7C4A-8B31-4CBBE3C99A22"); err == nil {
		t.Fatal("uppercase UUID accepted")
	}
	date, err := ParseDate("2027-03-14")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, date, `"2027-03-14"`)
	dateTime, err := ParseDateTime("2027-03-14T10:15:30.120+01:00")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, dateTime, `"2027-03-14T09:15:30.12Z"`)
	duration, err := ParseDuration("1h30m")
	if err != nil {
		t.Fatal(err)
	}
	if duration.String() != "PT1H30M" {
		t.Fatalf("duration = %q", duration.String())
	}
	fraction, err := ParseDuration("1.000000001s")
	if err != nil {
		t.Fatal(err)
	}
	if fraction.String() != "PT1.000000001S" {
		t.Fatalf("fraction = %q", fraction.String())
	}
	size, err := ParseSize("2GiB")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, size, `"2147483648"`)
}

func TestContractDateTimeRejectsBroaderGoLexicalForms(t *testing.T) {
	for _, value := range []string{
		"2027-03-14T10:15:30,123Z",
		"2027-03-14T10:15:30.1234567890Z",
		" 2027-03-14T10:15:30Z",
		"2027-03-14T10:15:30Z ",
	} {
		if _, err := ParseDateTime(value); err == nil {
			t.Errorf("accepted non-conforming datetime %q", value)
		}
	}
	value, err := ParseDateTime("2027-03-14T10:15:30.123+01:00")
	if err != nil || value.String() != "2027-03-14T09:15:30.123Z" {
		t.Fatalf("offset datetime = %q, %v", value.String(), err)
	}
}

func TestContractDurationAndSizeRemainExactBeyondMachineIntegerRange(t *testing.T) {
	duration, err := ParseDuration("9223372036854775808ns")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, duration, `"P106751DT23H47M16.854775808S"`)

	size, err := ParseSize("18446744073709551616B")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, size, `"18446744073709551616"`)
}

func TestContractSizeAcceptsOnlyIntegralExactByteCounts(t *testing.T) {
	size, err := ParseSize("1.5KiB")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, size, `"1536"`)
	if _, err := ParseSize("0.1B"); err == nil {
		t.Fatal("fractional byte count was accepted")
	}
	if _, err := ParseSize("-0B"); err == nil {
		t.Fatal("signed size was accepted")
	}
}

func TestDecimalExponentIsBoundedBeforeExpansion(t *testing.T) {
	if _, err := ParseDecimal("1e1000000000"); err == nil {
		t.Fatal("huge positive exponent was accepted")
	}
	if _, err := ParseDecimal("1e-1000000000"); err == nil {
		t.Fatal("huge negative exponent was accepted")
	}
	value, err := ParseDecimal("1.25e3")
	if err != nil || value.String() != "1250" {
		t.Fatalf("decimal = %q, %v", value.String(), err)
	}
}

func TestContractScalarJSONRoundTrips(t *testing.T) {
	values := []any{
		mustScalar(ParseInt("9007199254740993")),
		mustScalar(ParseDecimal("12.3400")),
		mustScalar(ParseUUID("018f47a2-6f45-7c4a-8b31-4cbbe3c99a22")),
		mustScalar(ParseDate("2027-03-14")),
		mustScalar(ParseDateTime("2027-03-14T09:15:30.123Z")),
		mustScalar(ParseDuration("1.5s")),
		mustScalar(ParseSize("12B")),
		mustScalar(ParseURL("https://example.com/a")),
		mustScalar(ParseRelativePath("models/a")),
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %T: %v", value, err)
		}
		target := reflect.New(reflect.TypeOf(value))
		if err := json.Unmarshal(encoded, target.Interface()); err != nil {
			t.Fatalf("unmarshal %T from %s: %v", value, encoded, err)
		}
		reencoded, err := json.Marshal(target.Elem().Interface())
		if err != nil || string(reencoded) != string(encoded) {
			t.Fatalf("round trip %T: %s -> %s, %v", value, encoded, reencoded, err)
		}
	}
}

func TestContractScalarJSONRejectsNonCanonicalForms(t *testing.T) {
	for _, test := range []struct {
		name   string
		input  string
		target any
	}{
		{name: "decimal leading zeros", input: `"001.2300"`, target: new(Decimal)},
		{name: "decimal exponent", input: `"1e3"`, target: new(Decimal)},
		{name: "datetime offset", input: `"2027-03-14T10:15:30+01:00"`, target: new(DateTime)},
		{name: "datetime trailing fractional zero", input: `"2027-03-14T09:15:30.120Z"`, target: new(DateTime)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(test.input), test.target); err == nil {
				t.Fatalf("accepted non-canonical %s", test.input)
			}
		})
	}
}

func TestDecodeJSONObjectAndOptionalNullableSemantics(t *testing.T) {
	for _, input := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{} {}`} {
		if _, err := DecodeJSONObject([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	object, err := DecodeJSONObject([]byte(`{"x":null}`))
	if err != nil || string(object["x"]) != "null" {
		t.Fatalf("object=%v err=%v", object, err)
	}
	var nullable Nullable[string]
	if err := json.Unmarshal([]byte("null"), &nullable); err != nil || !nullable.Null {
		t.Fatalf("nullable=%+v err=%v", nullable, err)
	}
	optional := Some(NullOf[string]())
	encoded, err := json.Marshal(optional)
	if err != nil || string(encoded) != "null" {
		t.Fatalf("optional nullable=%s err=%v", encoded, err)
	}
	if _, err := json.Marshal(NoneOf[string]()); err == nil {
		t.Fatal("direct absent optional was encoded")
	}
}

func TestExactJSONCanonicalizationBoundsNumericExpansion(t *testing.T) {
	if _, err := canonicalizeExactJSON([]byte(`[1e1000000,1e1000000]`)); err == nil {
		t.Fatal("amplifying numeric exponents were accepted")
	}
}

func mustScalar[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func TestContractPathsAndURLs(t *testing.T) {
	if _, err := ParseRelativePath("../secret"); err == nil {
		t.Fatal("escaping path accepted")
	}
	pathValue, err := ParseRelativePath("models/Cafe\u0301")
	if err != nil {
		t.Fatal(err)
	}
	if pathValue != RelativePath("models/Caf\u00e9") {
		t.Fatalf("relative path = %q", pathValue)
	}
	if _, err := ParseRelativePath("models/a\x00b"); err == nil {
		t.Fatal("NUL-containing path accepted")
	}
	if _, err := ParseRelativePath("models/" + string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 path accepted")
	}
	value, err := ParseURL("HTTPS://Example.COM:443/a/../b")
	if err != nil {
		t.Fatal(err)
	}
	if value.String() != "https://example.com/b" {
		t.Fatalf("URL = %q", value.String())
	}
	value, err = ParseURL("https://bücher.example/%7euser?q=%2f%7e")
	if err != nil {
		t.Fatal(err)
	}
	if value.String() != "https://xn--bcher-kva.example/~user?q=%2F~" {
		t.Fatalf("IDNA URL = %q", value.String())
	}
	value, err = ParseURL("https://[2001:db8::1]/a")
	if err != nil {
		t.Fatal(err)
	}
	if value.String() != "https://[2001:db8::1]/a" {
		t.Fatalf("IPv6 URL = %q", value.String())
	}
	value, err = ParseURL("HTTPS://us%65r:p%2f@BÜCHER.example:443/a/../b?q=✓#fr%61g/%2f")
	if err != nil {
		t.Fatal(err)
	}
	if value.String() != "https://user:p%2F@xn--bcher-kva.example/b?q=%E2%9C%93#frag/%2F" {
		t.Fatalf("full URL = %q", value.String())
	}
	value, err = ParseURL("https://example.com/a/.")
	if err != nil || value.String() != "https://example.com/a/" {
		t.Fatalf("trailing dot URL = %q, %v", value.String(), err)
	}
	if _, err := ParseURL("https://[fe80::1%25en0]/"); err == nil {
		t.Fatal("IPv6 zone was accepted")
	}
	if _, err := ParseURL("urn:example:asset"); err == nil {
		t.Fatal("opaque URI was accepted as a network URL")
	}
}

func assertJSON(t *testing.T, value any, want string) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("JSON = %s, want %s", b, want)
	}
}
