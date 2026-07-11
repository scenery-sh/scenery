package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devreport"
)

type mappedContractInput struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	TraceID string   `json:"trace_id"`
	Session string   `json:"session"`
}

func TestContractSystemFailureUsesStandardProblemOutcome(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterEndpointChecked(&Endpoint{
		Service: "contract", Name: "Failure", Access: Public, Path: "/failure", Methods: []string{http.MethodGet},
		PayloadType: reflect.TypeFor[struct{}](), ResponseType: reflect.TypeFor[struct{}](),
		DecodeContractRequest: func(*http.Request, map[string]string) (ContractDecodedRequest, error) {
			return ContractDecodedRequest{Payload: struct{}{}}, nil
		},
		Invoke: func(context.Context, []any, any) (any, error) {
			return nil, ContractSystemError(errors.New("boom"))
		},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/failure", nil))
	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("response = %d %#v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestContractResponseMetadataUsesSchemaEncodingAndDeclaredWireMode(t *testing.T) {
	encode := func(any) ([]byte, error) { return []byte(`["2","10"]`), nil }
	repeated := ContractHTTPResponse{}
	if err := AddContractResponseHeader(&repeated, "x-value", []string{"ignored"}, ContractResponseValueOptions{Encoding: "repeated", EncodeValue: encode}); err != nil {
		t.Fatal(err)
	}
	if got := repeated.Headers.Values("x-value"); !reflect.DeepEqual(got, []string{"2", "10"}) {
		t.Fatalf("repeated values = %#v", got)
	}
	comma := ContractHTTPResponse{}
	if err := AddContractResponseHeader(&comma, "x-value", []string{"ignored"}, ContractResponseValueOptions{Encoding: "comma", EncodeValue: encode}); err != nil {
		t.Fatal(err)
	}
	if got := comma.Headers.Get("x-value"); got != "2,10" {
		t.Fatalf("comma value = %q", got)
	}
	jsonHeader := ContractHTTPResponse{}
	if err := AddContractResponseHeader(&jsonHeader, "x-object", struct{}{}, ContractResponseValueOptions{Encoding: "json", EncodeValue: func(any) ([]byte, error) { return []byte(`{"b":2,"a":1}`), nil }}); err != nil {
		t.Fatal(err)
	}
	if got := jsonHeader.Headers.Get("x-object"); got != `{"a":1,"b":2}` {
		t.Fatalf("JSON header = %q", got)
	}
}

func TestContractResponseCookiePercentEncodesAndValidatesExpiry(t *testing.T) {
	response := ContractHTTPResponse{}
	options := ContractResponseValueOptions{EncodeValue: func(any) ([]byte, error) { return []byte(`"hello world"`), nil }}
	if err := AddContractResponseCookie(&response, ContractResponseCookie{Name: "session", Path: "/app", Expires: "2027-03-14T09:15:30Z", Secure: true, HTTPOnly: true, SameSite: http.SameSiteLaxMode}, "ignored", options); err != nil {
		t.Fatal(err)
	}
	value := response.Headers.Get("Set-Cookie")
	for _, want := range []string{"session=hello%20world", "Path=/app", "Expires=Sun, 14 Mar 2027 09:15:30 GMT", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(value, want) {
			t.Fatalf("cookie %q missing %q", value, want)
		}
	}
	if err := AddContractResponseCookie(&ContractHTTPResponse{}, ContractResponseCookie{Name: "session", Expires: "tomorrow"}, "ignored", options); err == nil {
		t.Fatal("invalid expiry was accepted")
	}
}

func TestContractGatewayLimitsConfigureHTTPServerBoundary(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterEndpointChecked(&Endpoint{
		Service: "contract", Name: "Limits", Access: Public, Path: "/limits", Methods: []string{http.MethodGet},
		PayloadType: reflect.TypeFor[struct{}](), ResponseType: reflect.TypeFor[struct{}](), Invoke: func(context.Context, []any, any) (any, error) { return struct{}{}, nil },
		ContractPolicy: &ContractHTTPPolicy{MaxRequestHeaderBytes: 64 << 10, ReadTimeoutNanos: int64(3 * time.Second), WriteTimeoutNanos: int64(4 * time.Second), IdleTimeoutNanos: int64(5 * time.Second), AuthorizationStrategy: "public"},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if server.MaxHeaderBytes != 64<<10 || server.ReadTimeout != 3*time.Second || server.ReadHeaderTimeout != 3*time.Second || server.WriteTimeout != 4*time.Second || server.IdleTimeout != 5*time.Second {
		t.Fatalf("server limits = headers %d read %s header %s write %s idle %s", server.MaxHeaderBytes, server.ReadTimeout, server.ReadHeaderTimeout, server.WriteTimeout, server.IdleTimeout)
	}
}

func TestDecodeContractInputCombinesTypedHTTPMappings(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/items/unused?tag=a+b&tag=c%20d", strings.NewReader(`{"name":"roof"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Add("X-Trace-ID", " trace-1 ")
	request.Header.Add("Cookie", "session=a%2Bb+c")
	input, err := DecodeContractInput[mappedContractInput](request, map[string]string{"item_id": "roof%20one"}, ContractRequestSchema{
		Mappings: []ContractInputMapping{
			{Source: ContractSourcePath, Name: "item_id", Target: "id", Type: "string"},
			{Source: ContractSourceQuery, Name: "tag", Target: "tags", Type: "list(string)", Encoding: "repeated"},
			{Source: ContractSourceHeader, Name: "x-trace-id", Target: "trace_id", Type: "string"},
			{Source: ContractSourceCookie, Name: "session", Target: "session", Type: "string"},
		},
		Body: &ContractBodyMapping{Codec: "json", Include: []string{"name"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := mappedContractInput{ID: "roof one", Name: "roof", Tags: []string{"a+b", "c d"}, TraceID: "trace-1", Session: "a+b+c"}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input = %#v, want %#v", input, want)
	}
}

func TestDirectExactIntegerBodiesUseSchemaDirectedStrings(t *testing.T) {
	decodeInt64 := func(data []byte, target any) error {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		*target.(*int64) = value
		return nil
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`"9223372036854775807"`))
	request.Header.Set("Content-Type", "application/json")
	decoded, err := DecodeContractInput[int64](request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json", Type: "int64", DecodeValue: decodeInt64}})
	if err != nil || decoded != int64(9223372036854775807) {
		t.Fatalf("decoded int64 = %d, %v", decoded, err)
	}

	unsafeNumber := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`9223372036854775807`))
	unsafeNumber.Header.Set("Content-Type", "application/json")
	if _, err := DecodeContractInput[int64](unsafeNumber, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json", Type: "int64", DecodeValue: decodeInt64}}); err == nil {
		t.Fatal("direct int64 body accepted a JSON number")
	}

	response, err := EncodeContractRepresentationWithOptions(nil, http.StatusOK, uint64(18446744073709551615), "json", []string{"application/json"}, ContractResponseOptions{TypeExpression: "uint64", EncodeValue: func(value any) ([]byte, error) {
		return json.Marshal(strconv.FormatUint(value.(uint64), 10))
	}})
	if err != nil || string(response.Body) != `"18446744073709551615"` {
		t.Fatalf("encoded uint64 response = %q, %v", response.Body, err)
	}
}

func TestDecodeContractInputRejectsAmbiguousWireValues(t *testing.T) {
	tests := []struct {
		name    string
		request *http.Request
		paths   map[string]string
		schema  ContractRequestSchema
	}{
		{
			name:    "encoded slash",
			request: httptest.NewRequest(http.MethodGet, "/", nil),
			paths:   map[string]string{"id": "a%2Fb"},
			schema:  ContractRequestSchema{Mappings: []ContractInputMapping{{Source: ContractSourcePath, Name: "id", Target: "id", Type: "string"}}},
		},
		{
			name:    "repeated scalar",
			request: httptest.NewRequest(http.MethodGet, "/?id=one&id=two", nil),
			schema:  ContractRequestSchema{Mappings: []ContractInputMapping{{Source: ContractSourceQuery, Name: "id", Target: "id", Type: "string"}}},
		},
		{
			name: "duplicate nested JSON member",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"roof","nested":{"a":1,"a":2}}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			schema: ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json"}},
		},
		{
			name: "excluded body field",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"untrusted"}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			schema: ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json", Except: []string{"id"}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeContractInput[map[string]any](test.request, test.paths, test.schema); err == nil {
				t.Fatal("ambiguous request was accepted")
			}
		})
	}
}

func TestContractTransportErrorsUseProfileStatuses(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	if err := RegisterEndpointChecked(&Endpoint{
		Service: "contract", Name: "Negotiate", Access: Public, Path: "/contract/:id", Methods: []string{http.MethodPost},
		PayloadType: reflect.TypeFor[mappedContractInput](), ResponseType: reflect.TypeFor[mappedContractInput](),
		DecodeContractRequest: func(request *http.Request, paths map[string]string) (ContractDecodedRequest, error) {
			input, err := DecodeContractInput[mappedContractInput](request, paths, ContractRequestSchema{
				Mappings: []ContractInputMapping{{Source: ContractSourcePath, Name: "id", Target: "id", Type: "string"}},
				Body:     &ContractBodyMapping{Codec: "json", Include: []string{"name"}},
			})
			return ContractDecodedRequest{Payload: input}, err
		},
		Invoke: func(_ context.Context, _ []any, payload any) (any, error) { return payload, nil },
		EncodeContractOutcome: func(request *http.Request, outcome any) (ContractHTTPResponse, error) {
			return EncodeContractJSONForRequest(request, http.StatusOK, outcome, []string{"application/json"}, 0)
		},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	unsupported := httptest.NewRecorder()
	unsupportedRequest := httptest.NewRequest(http.MethodPost, "/contract/one", strings.NewReader(`{"name":"roof"}`))
	unsupportedRequest.Header.Set("Content-Type", "text/plain")
	server.Handler.ServeHTTP(unsupported, unsupportedRequest)
	if unsupported.Code != http.StatusUnsupportedMediaType || unsupported.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unsupported response = %d %#v %q", unsupported.Code, unsupported.Header(), unsupported.Body.String())
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodPost, "/contract/one", strings.NewReader(`{"name":"roof"}`))
	server.Handler.ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusUnsupportedMediaType || missing.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("missing content type response = %d %#v %q", missing.Code, missing.Header(), missing.Body.String())
	}

	unacceptable := httptest.NewRecorder()
	unacceptableRequest := httptest.NewRequest(http.MethodPost, "/contract/one", strings.NewReader(`{"name":"roof"}`))
	unacceptableRequest.Header.Set("Content-Type", "application/json")
	unacceptableRequest.Header.Set("Accept", "image/png")
	server.Handler.ServeHTTP(unacceptable, unacceptableRequest)
	if unacceptable.Code != http.StatusNotAcceptable || unacceptable.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unacceptable response = %d %#v %q", unacceptable.Code, unacceptable.Header(), unacceptable.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(unacceptable.Body.Bytes(), &problem); err != nil || problem.Code != "transport.not_acceptable" {
		t.Fatalf("problem = %#v, err = %v", problem, err)
	}
}

func TestContractTransportResponseAndTraceStatusesMatch(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 16)}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	if err := RegisterEndpointChecked(&Endpoint{
		Service: "contract", Name: "TraceStatus", Access: Public, Path: "/trace-status", Methods: []string{http.MethodPost},
		PayloadType: reflect.TypeFor[mappedContractInput](), ResponseType: reflect.TypeFor[mappedContractInput](),
		DecodeContractRequest: func(request *http.Request, paths map[string]string) (ContractDecodedRequest, error) {
			input, err := DecodeContractInput[mappedContractInput](request, paths, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json", Include: []string{"name"}}})
			return ContractDecodedRequest{Payload: input}, err
		},
		Invoke: func(_ context.Context, _ []any, payload any) (any, error) {
			if payload.(mappedContractInput).Name == "dispatch" {
				return nil, &ContractTransportError{Outcome: "dispatch.unavailable", Status: http.StatusServiceUnavailable, Message: "unavailable"}
			}
			return payload, nil
		},
		EncodeContractOutcome: func(request *http.Request, outcome any) (ContractHTTPResponse, error) {
			return EncodeContractJSONForRequest(request, http.StatusOK, outcome, []string{"application/json"}, 0)
		},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		contentType string
		accept      string
		body        string
		want        int
	}{
		{name: "decode", contentType: "text/plain", body: `{"name":"roof"}`, want: http.StatusUnsupportedMediaType},
		{name: "encode", contentType: "application/json", accept: "image/png", body: `{"name":"roof"}`, want: http.StatusNotAcceptable},
		{name: "dispatch", contentType: "application/json", body: `{"name":"dispatch"}`, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/trace-status", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("response status = %d, want %d", recorder.Code, test.want)
			}
			start, end, summary := <-reporter.queue, <-reporter.queue, <-reporter.queue
			if start.Type != "trace-event" || end.Type != "trace-event" || summary.Type != "trace-summary" {
				t.Fatalf("reports = %#v %#v %#v", start, end, summary)
			}
			spanEnd := end.TraceEvent.Event["span_end"].(map[string]any)
			tracedRequest := spanEnd["request"].(map[string]any)
			if tracedRequest["http_status_code"] != test.want {
				t.Fatalf("trace status = %#v, want %d", tracedRequest["http_status_code"], test.want)
			}
		})
	}
}
