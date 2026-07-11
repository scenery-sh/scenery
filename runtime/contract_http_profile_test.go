package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeContractFormUsesDeclaredTypedFields(t *testing.T) {
	type input struct {
		Name  string   `json:"name"`
		Count int32    `json:"count"`
		Tags  []string `json:"tags"`
	}
	schema := ContractRequestSchema{Body: &ContractBodyMapping{Codec: "form", Fields: []ContractInputMapping{
		{Name: "name", Target: "name", Type: "string"},
		{Name: "count", Target: "count", Type: "int32"},
		{Name: "tag", Target: "tags", Type: "list(string)"},
	}}}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=roof+one&count=2&tag=a&tag=b"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	decoded, err := DecodeContractInput[input](request, nil, schema)
	if err != nil || !reflect.DeepEqual(decoded, input{Name: "roof one", Count: 2, Tags: []string{"a", "b"}}) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=roof&count=2&tag=a&extra=x"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := DecodeContractInput[input](request, nil, schema); err == nil {
		t.Fatal("undeclared form field was accepted")
	}
}

func TestContractMappedExactIntegerWireFormsMatchGeneratedContracts(t *testing.T) {
	for _, test := range []struct {
		kind, input, want string
	}{
		{"int32", "42", `42`},
		{"uint32", "42", `42`},
		{"int64", "9007199254740993", `"9007199254740993"`},
		{"uint64", "18446744073709551615", `"18446744073709551615"`},
		{"int", "900719925474099312345", `"900719925474099312345"`},
		{"decimal", "12.34", `"12.34"`},
	} {
		raw, err := contractScalarJSON(test.kind, test.input, nil)
		if err != nil || string(raw) != test.want {
			t.Errorf("%s(%q) = %s, %v; want %s", test.kind, test.input, raw, err, test.want)
		}
	}
	if _, err := contractScalarJSON("decimal", "12.340", nil); err == nil {
		t.Fatal("non-canonical decimal was accepted")
	}
}

func TestDecodeContractMultipartEnforcesPartsAndLimits(t *testing.T) {
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="document"`)
	header.Set("Content-Type", "application/octet-stream")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("data"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	type input struct {
		Document []byte `json:"document"`
	}
	schema := ContractRequestSchema{Body: &ContractBodyMapping{Codec: "multipart", MaxMultipartParts: 1, MultipartParts: []ContractMultipartPart{{Name: "document", Target: "document", Type: "bytes", Kind: "bytes", MediaTypes: []string{"application/octet-stream"}, MaxBytes: 4}}}}
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	decoded, err := DecodeContractInput[input](request, nil, schema)
	if err != nil || string(decoded.Document) != "data" {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	schema.Body.MultipartParts[0].MaxBytes = 3
	request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if _, err := DecodeContractInput[input](request, nil, schema); err == nil {
		t.Fatal("multipart part limit was not enforced")
	}
}

func TestDecodeContractContextUsesAuthenticatedPrincipal(t *testing.T) {
	type input struct {
		UserID string `json:"user_id"`
	}
	ctx := WithAuthContext(context.Background(), AuthInfo{UID: "user-42"})
	restore := enterState(stateFromContext(ctx))
	defer restore()
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	decoded, err := DecodeContractInput[input](request, nil, ContractRequestSchema{ContextMappings: []ContractContextMapping{{Source: "principal.uid", Target: "user_id"}}})
	if err != nil || decoded.UserID != "user-42" {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}

func TestContractResponseCompressionIsPolicyBound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "gzip, identity;q=0.5")
	response, err := EncodeContractRepresentationWithOptions(request, 200, map[string]string{"value": strings.Repeat("x", 100)}, "json", []string{"application/json"}, ContractResponseOptions{MaxBytes: 1024, CompressionAlgorithms: []string{"gzip"}, CompressionThreshold: 1})
	if err != nil || response.Headers.Get("Content-Encoding") != "gzip" {
		t.Fatalf("headers=%v err=%v", response.Headers, err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(response.Body))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil || !bytes.Contains(decoded, []byte(`"value"`)) {
		t.Fatalf("decoded=%s err=%v", decoded, err)
	}
	request.Header.Set("Accept-Encoding", "identity;q=0, gzip;q=0")
	if _, err := EncodeContractRepresentationWithOptions(request, 200, "x", "json", []string{"application/json"}, ContractResponseOptions{CompressionAlgorithms: []string{"gzip"}, CompressionThreshold: 0}); err == nil {
		t.Fatal("unacceptable response encoding was accepted")
	}
	request.Header.Set("Accept-Encoding", "*;q=0")
	if _, err := EncodeContractRepresentationWithOptions(request, 200, "x", "json", []string{"application/json"}, ContractResponseOptions{CompressionAlgorithms: []string{"gzip"}, CompressionThreshold: 0}); err == nil {
		t.Fatal("wildcard exclusion incorrectly retained implicit identity")
	}
	request.Header.Set("Accept-Encoding", "identity;q=0, gzip;q=1")
	response, err = EncodeContractRepresentationWithOptions(request, 200, "small", "json", []string{"application/json"}, ContractResponseOptions{CompressionAlgorithms: []string{"gzip"}, CompressionThreshold: 1024})
	if err != nil || response.Headers.Get("Content-Encoding") != "gzip" {
		t.Fatalf("explicitly required below-threshold gzip = headers %v, err %v", response.Headers, err)
	}
	if _, err := EncodeContractRepresentationWithOptions(request, 200, "small", "json", []string{"application/json"}, ContractResponseOptions{CompressionThreshold: 1024}); err == nil {
		t.Fatal("identity refusal was ignored when no response compression is configured")
	}
}

func TestContractMediaTypeParametersAreMatched(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"roof"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8; profile=v2")
	_, err := DecodeContractInput[map[string]any](request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json", AcceptedMediaTypes: []string{"application/json; profile=v1"}}})
	if status, ok := contractTransportHTTPStatus(err); err == nil || !ok || status != http.StatusUnsupportedMediaType {
		t.Fatalf("request media parameter mismatch = %v, status %d/%t", err, status, ok)
	}

	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"roof"}`))
	request.Header.Set("Content-Type", "application/json; profile=v1; charset=utf-8")
	if _, err := DecodeContractInput[map[string]any](request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json", AcceptedMediaTypes: []string{"application/json; profile=v1"}}}); err != nil {
		t.Fatalf("matching request media parameters were rejected: %v", err)
	}

	responseRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	responseRequest.Header.Set("Accept", "application/json; profile=v2")
	if _, err := EncodeContractRepresentationForRequest(responseRequest, http.StatusOK, map[string]any{"ok": true}, "json", []string{"application/json; profile=v1"}, 0); err == nil {
		t.Fatal("response media parameter mismatch was accepted")
	}
	responseRequest.Header.Set("Accept", "application/json; profile=v1")
	if _, err := EncodeContractRepresentationForRequest(responseRequest, http.StatusOK, map[string]any{"ok": true}, "json", []string{"application/json; profile=v1"}, 0); err != nil {
		t.Fatalf("matching response media parameters were rejected: %v", err)
	}
}

func TestReadContractBodyRejectsTrailingGZIPData(t *testing.T) {
	first := contractGZIPMember(t, []byte("first"))
	for name, suffix := range map[string][]byte{
		"trailing bytes": []byte("trailing"),
		"second member":  contractGZIPMember(t, []byte("second")),
	} {
		t.Run(name, func(t *testing.T) {
			payload := append(append([]byte(nil), first...), suffix...)
			if _, err := readContractBody(bytes.NewReader(payload), "gzip", int64(len(payload)), 100, []string{"gzip"}); err == nil {
				t.Fatal("ambiguous gzip payload was accepted")
			}
		})
	}
}

func TestContractRequestDefaultBodyLimitsMatchHTTPProfile(t *testing.T) {
	if defaultContractRequestBytes != 8<<20 || defaultContractDecompressedRequestBytes != 16<<20 {
		t.Fatalf("defaults = compressed %d, decompressed %d", defaultContractRequestBytes, defaultContractDecompressedRequestBytes)
	}
	if _, err := readContractBody(bytes.NewReader(make([]byte, defaultContractRequestBytes+1)), "", defaultContractRequestBytes, defaultContractDecompressedRequestBytes, nil); err == nil {
		t.Fatal("buffered request exceeded the 8 MiB compressed limit")
	}
	compressed := contractGZIPMember(t, make([]byte, defaultContractDecompressedRequestBytes+1))
	if _, err := readContractBody(bytes.NewReader(compressed), "gzip", defaultContractRequestBytes, defaultContractDecompressedRequestBytes, []string{"gzip"}); err == nil {
		t.Fatal("compressed request exceeded the 16 MiB decompressed limit")
	}
}

func contractGZIPMember(t *testing.T, payload []byte) []byte {
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

func TestContractForwardedAndCORSPoliciesFailClosed(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://service.test/", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	request.Header.Set("Forwarded", "for=198.51.100.2")
	request.Header.Set("X-Forwarded-Host", "evil.test")
	applyContractForwardedPolicy(request, &ContractHTTPPolicy{Forwarded: "trusted", TrustedProxyPrefixes: []string{"10.0.0.0/8"}})
	if request.Header.Get("Forwarded") == "" || request.Header.Get("X-Forwarded-Host") == "" {
		t.Fatal("untrusted forwarding metadata did not remain available as ordinary headers")
	}
	if request.Host != "service.test" || request.RemoteAddr != "203.0.113.10:1234" {
		t.Fatal("untrusted forwarding metadata altered authoritative request context")
	}

	trusted := httptest.NewRequest(http.MethodGet, "http://service.test/", nil)
	trusted.RemoteAddr = "10.1.2.3:4321"
	trusted.Header.Set("Forwarded", `for=198.51.100.7;proto=https;host=api.example.test`)
	applyContractForwardedPolicy(trusted, &ContractHTTPPolicy{Forwarded: "accept", TrustedProxyPrefixes: []string{"10.0.0.0/8"}})
	if trusted.RemoteAddr != "198.51.100.7:0" || trusted.Host != "api.example.test" || trusted.URL.Scheme != "https" {
		t.Fatalf("trusted forwarding context = remote %q host %q scheme %q", trusted.RemoteAddr, trusted.Host, trusted.URL.Scheme)
	}
	if trusted.Header.Get("Forwarded") == "" {
		t.Fatal("trusted forwarding metadata did not remain available as an ordinary header")
	}

	headers := http.Header{"Access-Control-Allow-Origin": []string{"https://old.test"}}
	request.Header.Set("Origin", "https://app.test")
	applyContractCORSHeaders(headers, request, &ContractHTTPPolicy{CORS: "none"})
	if headers.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS none retained an allow origin")
	}
	applyContractCORSHeaders(headers, request, &ContractHTTPPolicy{CORS: "explicit", AllowedOrigins: []string{"https://app.test"}})
	if headers.Get("Access-Control-Allow-Origin") != "https://app.test" {
		t.Fatalf("CORS headers=%v", headers)
	}
}
