package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type constrainedPathTailInput struct {
	Path string `json:"path"`
}

func (value *constrainedPathTailInput) UnmarshalJSON(data []byte) error {
	type wire constrainedPathTailInput
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if len(decoded.Path) < 2 {
		return fmt.Errorf("path minimum length is 2")
	}
	*value = constrainedPathTailInput(decoded)
	return nil
}

func TestPathTailRouterCardinalityAndPrecedence(t *testing.T) {
	router := newRouteTable()
	selected := ""
	handle := func(name string) routeHandle {
		return func(w http.ResponseWriter, _ *http.Request, _ routeParams) {
			selected = name
			w.WriteHeader(http.StatusNoContent)
		}
	}
	router.HandlePathTail([]string{http.MethodGet}, "/drive/*path", handle("tail"))
	router.HandlePathTail([]string{http.MethodGet}, "/drive/public/*path", handle("public-tail"))
	router.Handle([]string{http.MethodGet}, "/drive/:bucket", handle("parameter"))
	router.Handle([]string{http.MethodGet}, "/drive/health", handle("literal"))
	router.Handle([]string{http.MethodGet}, "/drive", handle("exact"))

	tests := []struct {
		path string
		want string
		code int
	}{
		{"/drive", "exact", http.StatusNoContent},
		{"/drive/health", "literal", http.StatusNoContent},
		{"/drive/photos", "parameter", http.StatusNoContent},
		{"/drive/public/logo.svg", "public-tail", http.StatusNoContent},
		{"/drive/photos/2027/logo.svg", "tail", http.StatusNoContent},
		{"/drive/", "", http.StatusNotFound},
		{"/drive//logo.svg", "", http.StatusNotFound},
	}
	for _, test := range tests {
		selected = ""
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.code || selected != test.want {
			t.Errorf("%s = status %d route %q, want %d %q", test.path, recorder.Code, selected, test.code, test.want)
		}
	}
}

func TestPathTailRouterDoesNotFallBackAfterSelection(t *testing.T) {
	router := newRouteTable()
	tailCalled := false
	router.HandlePathTail([]string{http.MethodGet}, "/drive/*path", func(w http.ResponseWriter, _ *http.Request, _ routeParams) {
		tailCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	router.Handle([]string{http.MethodGet}, "/drive/:bucket", func(w http.ResponseWriter, _ *http.Request, _ routeParams) {
		http.Error(w, "invalid selected parameter", http.StatusBadRequest)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/drive/%2e%2e", nil))
	if recorder.Code != http.StatusBadRequest || tailCalled {
		t.Fatalf("selected route status=%d tailCalled=%t", recorder.Code, tailCalled)
	}
}

func TestDecodeContractPathTail(t *testing.T) {
	valid := map[string]string{
		"assets/logo.svg":       "assets/logo.svg",
		"space%20here/a+b":      "space here/a+b",
		"caf%C3%A9/menu":        "café/menu",
		"literal%25percent":     "literal%percent",
		"%2Ejson/manifest":      ".json/manifest",
		"reserved%3Avalue/file": "reserved:value/file",
	}
	for encoded, want := range valid {
		got, err := decodeContractPathTail(encoded, ContractInputMapping{Type: "string"})
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("decode %q = %#v, %v; want %q", encoded, got, err, want)
		}
	}
	for _, encoded := range []string{
		"a//b", ".", "..", "%2e", "%2E%2e", "a%2fb", "a%2Fb", "a%5cb", "a\\b", "%00", "%", "%C0%AF",
		"%252f", "%255C", "%2500", "%252e%252e",
	} {
		if _, err := decodeContractPathTail(encoded, ContractInputMapping{Type: "string"}); err == nil {
			t.Errorf("unsafe tail %q was accepted", encoded)
		}
	}
	if got, err := decodeContractPathTail("", ContractInputMapping{Type: "string"}); err != nil || len(got) != 1 || got[0] != "" {
		t.Fatalf("empty string tail = %#v, %v", got, err)
	}
	if got, err := decodeContractPathTail("", ContractInputMapping{Type: "optional(relative_path)", Optional: true}); err != nil || got != nil {
		t.Fatalf("empty optional tail = %#v, %v", got, err)
	}
	if _, err := decodeContractPathTail("", ContractInputMapping{Type: "relative_path"}); err == nil {
		t.Fatal("empty relative_path tail was accepted")
	}
}

func TestContractPathTailRunsTargetValidationAfterDecoding(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/drive/%61", nil)
	_, err := DecodeContractInput[constrainedPathTailInput](request, map[string]string{"path": "%61"}, ContractRequestSchema{Mappings: []ContractInputMapping{{Source: ContractSourcePathTail, Name: "path", Target: "path", Type: "string"}}})
	if err == nil || !strings.Contains(err.Error(), "minimum length") {
		t.Fatalf("decoded target constraint error = %v", err)
	}
}

func TestContractPathTailPopulatesTypedInputAndRejectsUnsafePaths(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	type input struct {
		Path string `json:"path"`
	}
	var invoked []string
	if err := RegisterEndpointChecked(&Endpoint{
		Service: "drive", Name: "Download", Access: Public, Path: "/drive/*path", Methods: []string{http.MethodGet},
		ContractPathTail: testContractPathTail("path", "string"),
		DecodeContractRequest: func(request *http.Request, paths map[string]string) (ContractDecodedRequest, error) {
			value, err := DecodeContractInput[input](request, paths, ContractRequestSchema{Mappings: []ContractInputMapping{{Source: ContractSourcePathTail, Name: "path", Target: "path", Type: "string"}}})
			return ContractDecodedRequest{Payload: value}, err
		},
		Invoke: func(_ context.Context, _ []any, payload any) (any, error) {
			invoked = append(invoked, payload.(input).Path)
			return struct{}{}, nil
		},
		EncodeContractOutcome: func(*http.Request, any) (ContractHTTPResponse, error) {
			return ContractHTTPResponse{Status: http.StatusNoContent}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		code int
	}{
		{"/drive", http.StatusNoContent},
		{"/drive/assets/space%20here/a+b", http.StatusNoContent},
		{"/drive/", http.StatusNotFound},
		{"/drive/a%2Fb", http.StatusBadRequest},
		{"/drive/%252e%252e", http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.code {
			t.Errorf("%s status=%d body=%q, want %d", test.path, recorder.Code, recorder.Body.String(), test.code)
		}
	}
	if !reflect.DeepEqual(invoked, []string{"", "assets/space here/a+b"}) {
		t.Fatalf("invoked inputs = %#v", invoked)
	}
}

func TestContractPathTailRegistrationRejectsEqualRouteSets(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	first := completeTestEndpoint(&Endpoint{Service: "drive", Name: "Download", Access: Public, Path: "/drive/*path", Methods: []string{http.MethodGet}, ContractPathTail: testContractPathTail("path", "string")})
	if err := RegisterEndpointChecked(first); err != nil {
		t.Fatal(err)
	}
	conflict := completeTestEndpoint(&Endpoint{Service: "drive", Name: "Delete", Access: Public, Path: "/drive/*rest", Methods: []string{http.MethodHead}, ContractPathTail: testContractPathTail("rest", "string")})
	if err := RegisterEndpointChecked(conflict); err == nil {
		t.Fatal("equal path-tail match set was registered")
	}
	nonConflict := completeTestEndpoint(&Endpoint{Service: "drive", Name: "Bucket", Access: Public, Path: "/drive/:bucket", Methods: []string{http.MethodGet}})
	if err := RegisterEndpointChecked(nonConflict); err != nil {
		t.Fatalf("single-segment parameter should coexist with tail: %v", err)
	}
}

func TestContractPathTailRegistrationRejectsInvalidMetadata(t *testing.T) {
	valid := testContractPathTail("path", "string")
	tests := []struct {
		name   string
		mutate func(*ContractPathTail)
	}{
		{"canonical template", func(tail *ContractPathTail) { tail.CanonicalTemplate = "/drive/{rest...}" }},
		{"target type", func(tail *ContractPathTail) { tail.Type = "optional(string)" }},
		{"precedence", func(tail *ContractPathTail) { tail.Precedence = []string{"path_tail", "literal"} }},
		{"profiles", func(tail *ContractPathTail) { tail.RequiredProfiles = []string{contractHTTPPathTailProfile} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := *valid
			candidate.Precedence = append([]string(nil), valid.Precedence...)
			candidate.RequiredProfiles = append([]string(nil), valid.RequiredProfiles...)
			test.mutate(&candidate)
			if err := validateContractPathTail(&Endpoint{Path: "/drive/*path", ContractPathTail: &candidate}); err == nil {
				t.Fatal("invalid runtime metadata was accepted")
			}
		})
	}
}

func completeTestEndpoint(endpoint *Endpoint) *Endpoint {
	endpoint.DecodeContractRequest = func(*http.Request, map[string]string) (ContractDecodedRequest, error) {
		return ContractDecodedRequest{}, nil
	}
	endpoint.Invoke = func(context.Context, []any, any) (any, error) { return struct{}{}, nil }
	endpoint.EncodeContractOutcome = func(*http.Request, any) (ContractHTTPResponse, error) {
		return ContractHTTPResponse{Status: http.StatusNoContent}, nil
	}
	return endpoint
}

func TestContractPathTailCORSUsesSelectedMethodAndRoutePrecedence(t *testing.T) {
	exact, tail := &ContractHTTPPolicy{}, &ContractHTTPPolicy{}
	server := &server{contractCORS: []contractCORSRoute{
		{path: "/drive/*path", pathTail: true, methods: []string{http.MethodDelete}, policy: tail},
		{path: "/drive/health", methods: []string{http.MethodGet}, policy: exact},
	}}
	if got := server.contractCORSPolicy("/drive/health", http.MethodGet); got != exact {
		t.Fatalf("GET policy = %p, want exact %p", got, exact)
	}
	if got := server.contractCORSPolicy("/drive/health", http.MethodDelete); got != tail {
		t.Fatalf("DELETE policy = %p, want tail %p", got, tail)
	}
	if got := server.contractCORSPolicy("/drive/", http.MethodDelete); got != nil {
		t.Fatalf("trailing-slash policy = %p, want nil", got)
	}
}

func testContractPathTail(name, targetType string) *ContractPathTail {
	empty := map[string]string{"string": "empty_string", "relative_path": "invalid_request", "optional(relative_path)": "absent"}[targetType]
	return &ContractPathTail{
		CanonicalTemplate: "/drive/{" + name + "...}", Name: name, Target: "operation.download.input.path", Type: targetType,
		EmptyCapture: empty, Decoding: "segment_rfc3986_once", Guarantee: "framework_enforced",
		Precedence:       []string{"literal", "parameter", "exact_end", "path_tail"},
		RequiredProfiles: []string{contractHTTPPathTailProfile, contractRuntimeHTTPPathTailProfile},
	}
}
