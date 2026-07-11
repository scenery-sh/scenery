package vnext

import (
	"bytes"
	"compress/gzip"
	"mime/multipart"
	"net/textproto"
	"reflect"
	"testing"
)

func TestDecodeHTTPPathSegmentRejectsStructuralEscapes(t *testing.T) {
	for _, value := range []string{"a%2Fb", "a%5Cb", "%2e%2e", "%00", "%ff"} {
		if _, err := DecodeHTTPPathSegment(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	value, err := DecodeHTTPPathSegment("roof%20one")
	if err != nil || value != "roof one" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}

func TestDecodeHTTPJSONRejectsBoundaryAmbiguity(t *testing.T) {
	for _, input := range [][]byte{
		[]byte(`{"a":1,"a":2}`),
		[]byte("\xef\xbb\xbf{}"),
		[]byte(`{"a":"\ud800"}`),
		[]byte(`{} trailing`),
		{0xff},
	} {
		if _, err := DecodeHTTPJSON(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	value, err := DecodeHTTPJSON([]byte(`{"n":9007199254740993,"s":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`9007199254740993`)) {
		t.Fatalf("number lost precision: %s", canonical)
	}
}

func TestNegotiateHTTPMediaUsesQualitySpecificityAndDeclarationOrder(t *testing.T) {
	produced := []string{"application/problem+json", "application/json", "text/plain"}
	media, err := NegotiateHTTPMedia("application/*;q=0.8, application/json;q=0.8, text/*;q=0.9", produced)
	if err != nil || media != "text/plain" {
		t.Fatalf("media=%q err=%v", media, err)
	}
	media, err = NegotiateHTTPMedia("application/*;q=0.8, application/json;q=0.8", produced)
	if err != nil || media != "application/json" {
		t.Fatalf("specific media=%q err=%v", media, err)
	}
	media, err = NegotiateHTTPMedia("application/*", produced)
	if err != nil || media != "application/problem+json" {
		t.Fatalf("declaration order media=%q err=%v", media, err)
	}
	if _, err := NegotiateHTTPMedia("image/png", produced); err == nil {
		t.Fatal("accepted unavailable media")
	}
}

func TestDecodeHTTPHeaderAndCookieCardinality(t *testing.T) {
	value, err := DecodeHTTPHeader([]string{"  a  ", "b"}, "list(string)")
	if err != nil || !reflect.DeepEqual(value, []any{"a", "b"}) {
		t.Fatalf("header=%#v err=%v", value, err)
	}
	if _, err := DecodeHTTPHeader([]string{"a", "b"}, "string"); err == nil {
		t.Fatal("repeated scalar header accepted")
	}
	value, err = DecodeHTTPCookie("a%2Bb+c", "string")
	if err != nil || value != "a+b+c" {
		t.Fatalf("cookie=%#v err=%v", value, err)
	}
}

func TestReadHTTPBodyEnforcesCompressedAndExpandedLimits(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(bytes.Repeat([]byte("a"), 1000))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := ReadHTTPBody(bytes.NewReader(compressed.Bytes()), "gzip", int64(compressed.Len()), 1000, []string{"gzip"})
	if err != nil || len(data) != 1000 {
		t.Fatalf("len=%d err=%v", len(data), err)
	}
	if _, err := ReadHTTPBody(bytes.NewReader(compressed.Bytes()), "gzip", int64(compressed.Len()-1), 1000, []string{"gzip"}); err == nil {
		t.Fatal("compressed limit was not enforced")
	}
	if _, err := ReadHTTPBody(bytes.NewReader(compressed.Bytes()), "gzip", int64(compressed.Len()), 999, []string{"gzip"}); err == nil {
		t.Fatal("expanded limit was not enforced")
	}
	if _, err := ReadHTTPBody(bytes.NewReader(compressed.Bytes()), "br", 1000, 1000, []string{"gzip"}); err == nil {
		t.Fatal("unsupported content encoding was accepted")
	}
	for name, suffix := range map[string][]byte{
		"trailing bytes": []byte("trailing"),
		"second member":  gzipMember(t, []byte("second")),
	} {
		t.Run(name, func(t *testing.T) {
			payload := append(append([]byte(nil), compressed.Bytes()...), suffix...)
			if _, err := ReadHTTPBody(bytes.NewReader(payload), "gzip", int64(len(payload)), 2000, []string{"gzip"}); err == nil {
				t.Fatal("ambiguous gzip payload was accepted")
			}
		})
	}
}

func gzipMember(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestDecodeHTTPMultipartEnforcesSchema(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="document"; filename="a.txt"`)
	header.Set("Content-Type", "text/plain")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("hello"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	values, err := DecodeHTTPMultipart(body.Bytes(), writer.FormDataContentType(), []HTTPMultipartPart{{Name: "document", Kind: "file", MediaTypes: []string{"text/plain"}, MaxBytes: 5, RetainFilename: true}}, 1)
	if err != nil || len(values["document"]) != 1 || values["document"][0].Filename != "a.txt" || string(values["document"][0].Bytes) != "hello" {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	if _, err := DecodeHTTPMultipart(body.Bytes(), writer.FormDataContentType(), nil, 1); err == nil {
		t.Fatal("undeclared part was accepted")
	}
	if _, err := DecodeHTTPMultipart(body.Bytes(), writer.FormDataContentType(), []HTTPMultipartPart{{Name: "document", Kind: "file", MediaTypes: []string{"text/plain"}, MaxBytes: 4}}, 1); err == nil {
		t.Fatal("part limit was not enforced")
	}
}

func TestDecodeHTTPQueryCardinality(t *testing.T) {
	if _, err := DecodeHTTPQuery([]string{"a", "b"}, "string"); err == nil {
		t.Fatal("repeated scalar accepted")
	}
	list, err := DecodeHTTPQuery([]string{"a", "b"}, "list(string)")
	if err != nil || !reflect.DeepEqual(list, []any{"a", "b"}) {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	if _, err := DecodeHTTPQuery([]string{"a", "a"}, "set(string)"); err == nil {
		t.Fatal("duplicate set element accepted")
	}
}

func TestHTTPDurationUsesISO8601ElapsedForm(t *testing.T) {
	value, err := DecodeHTTPScalar("duration", "P1DT2H3M4.000000005S")
	if err != nil {
		t.Fatal(err)
	}
	if value.(interface{ String() string }).String() != "P1DT2H3M4.000000005S" {
		t.Fatalf("value=%v", value)
	}
	if _, err := DecodeHTTPScalar("duration", "1h"); err == nil {
		t.Fatal("source duration accepted on HTTP")
	}
	day, err := DecodeHTTPScalar("duration", "P1D")
	if err != nil || day.(interface{ String() string }).String() != "P1D" {
		t.Fatalf("day=%v err=%v", day, err)
	}
	if _, err := DecodeHTTPScalar("duration", "P1DT"); err == nil {
		t.Fatal("duration with empty time component accepted")
	}
}

func TestHTTPScalarCanonicalForms(t *testing.T) {
	valid := map[string]string{
		"bool": "true", "int": "-12", "int32": "12", "uint32": "12", "int64": "12", "uint64": "12",
		"decimal": "12.34", "float32": "1.5", "float64": "1.5", "bytes": "AQI=",
		"uuid": "018f47a2-6f45-7c4a-8b31-4cbbe3c99a22", "date": "2027-03-14",
		"datetime": "2027-03-14T09:15:30.123Z", "duration": "PT1.5S", "size": "12",
		"url": "https://example.com/a", "relative_path": "models/a",
	}
	for kind, value := range valid {
		if _, err := DecodeHTTPScalar(kind, value); err != nil {
			t.Errorf("%s %q: %v", kind, value, err)
		}
	}
	invalid := map[string][]string{
		"int32": {"+1", "01", "-0"}, "uint64": {"+1", "01", "-1"},
		"decimal": {"1.0", "01", "-0"}, "float64": {"1.0", "+1", "-0", "NaN", "Inf"},
		"bytes": {"AQI", "AR=="}, "datetime": {"2027-03-14T10:15:30+01:00"},
		"duration": {"PT1.0S"}, "size": {"01"}, "url": {"HTTPS://EXAMPLE.COM:443/a/../b"},
	}
	for kind, values := range invalid {
		for _, value := range values {
			if _, err := DecodeHTTPScalar(kind, value); err == nil {
				t.Errorf("accepted non-canonical %s %q", kind, value)
			}
		}
	}
}

func TestDecodeRawHTTPQueryUsesRFC3986AndExplicitCollections(t *testing.T) {
	values, err := DecodeRawHTTPQuery("tag=a+b&tag=c%20d", "tag", "list(string)", "repeated")
	if err != nil || !reflect.DeepEqual(values, []any{"a+b", "c d"}) {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	values, err = DecodeRawHTTPQuery("tag=a%2Cb,c", "tag", "list(string)", "comma")
	if err != nil || !reflect.DeepEqual(values, []any{"a,b", "c"}) {
		t.Fatalf("comma values=%#v err=%v", values, err)
	}
	if _, err := DecodeRawHTTPQuery("tag=%zz", "tag", "string", ""); err == nil {
		t.Fatal("invalid percent escape accepted")
	}
	jsonValue, err := DecodeRawHTTPQuery("filter=%7B%22n%22%3A9007199254740993%7D", "filter", "json", "json")
	if err != nil || jsonValue.(map[string]any)["n"].(map[string]any)["coefficient"] != "9007199254740993" {
		t.Fatalf("JSON query=%#v err=%v", jsonValue, err)
	}
}
