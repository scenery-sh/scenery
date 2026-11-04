package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDocumentJSONPreservesWireBytes(t *testing.T) {
	for _, raw := range []string{`9007199254740993.123456789`, `1e400`, `{"b":[],"a":null}`, `"<>&\u2028\u0041"`, `"háček 😀"`} {
		document := NewDocument("json-test", "local")
		document.Values["module.value"] = json.RawMessage(raw)
		document.Revision = document.contentRevision()
		want, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := document.encode()
		if err != nil || !bytes.Equal(encoded, append(want, '\n')) {
			t.Fatalf("encode %s = %s, %v; want %s", raw, encoded, err, want)
		}
		// Feed the original tokens directly; pretty rendering may change their
		// whitespace and HTML escaping, which are separate output assertions.
		compact := fmt.Appendf(nil, `{"kind":%q,"schema_revision":%q,"app_id":%q,"environment":%q,"revision":%q,"values":{"module.value":%s},"secrets":{}}`,
			document.Kind, document.SchemaRevision, document.AppID, document.Environment, document.Revision, raw)
		decoded, err := DecodeDocument(compact, document.AppID, document.Environment)
		if err != nil || string(decoded.Values["module.value"]) != raw {
			t.Fatalf("decode %s = %s, %v", raw, decoded.Values["module.value"], err)
		}
	}
}

func TestDocumentJSONRejectsAmbiguousInputs(t *testing.T) {
	document := NewDocument("json-test", "local")
	encoded, err := document.encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		strings.Replace(string(encoded), `"app_id":`, `"App_id":`, 1),
		strings.Replace(string(encoded), `"values": {}`, `"values": {"module.value":{"a":1,"\u0061":2}}`, 1),
		strings.Replace(string(encoded), `"values": {}`, `"values": {"module.value":"\ud800"}`, 1),
		strings.Replace(string(encoded), `"values": {}`, "\"values\": {\"module.value\":\"\xff\"}", 1),
		string(encoded) + `{}`,
	} {
		if _, err := DecodeDocument([]byte(malformed), document.AppID, document.Environment); err == nil {
			t.Fatalf("accepted %q", malformed)
		}
	}
}

func TestDocumentJSONPreservesNilMapEncoding(t *testing.T) {
	document := NewDocument("json-test", "local")
	document.Values, document.Secrets = nil, nil
	want, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := document.encode()
	if err != nil || !bytes.Equal(encoded, append(want, '\n')) {
		t.Fatalf("nil maps = %s, %v; want %s", encoded, err, want)
	}
}

func BenchmarkJSONV2Document(b *testing.B) {
	for _, size := range []int{16, 128} {
		document := NewDocument("json-bench", "local")
		for i := range size {
			document.Values[fmt.Sprintf("module.key_%d", i)] = json.RawMessage(`"/srv/models/representative-value"`)
		}
		document.Revision = document.contentRevision()
		encoded, err := document.encode()
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("encode/%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := document.encode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("decode/%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := DecodeDocument(encoded, document.AppID, document.Environment); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
