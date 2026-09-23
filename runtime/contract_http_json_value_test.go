package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scenery.sh/internal/contract"
)

type exactJSONDocumentInput struct {
	Document json.RawMessage `json:"document"`
	Filter   json.RawMessage `json:"filter,omitempty"`
}

// Empty arrays and objects inside `json` values are data, not absence: the
// HTTP boundary must deliver them to the handler exactly as sent, and their
// exact-JSON canonical hashes must stay distinct from null.
func TestContractJSONValuesPreserveEmptyCollectionsThroughHandler(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	var received []exactJSONDocumentInput
	if err := RegisterEndpointChecked(&Endpoint{
		Service: "contract", Name: "Document", Access: Public, Path: "/documents", Methods: []string{http.MethodPost},
		DecodeContractRequest: func(request *http.Request, paths map[string]string) (ContractDecodedRequest, error) {
			input, err := DecodeContractInput[exactJSONDocumentInput](request, paths, ContractRequestSchema{
				Mappings: []ContractInputMapping{{Source: ContractSourceHeader, Name: "x-filter", Target: "filter", Type: "json", Encoding: "json", Optional: true}},
				Body:     &ContractBodyMapping{Codec: "json", Include: []string{"document"}},
			})
			return ContractDecodedRequest{Payload: input}, err
		},
		Invoke: func(_ context.Context, _ []any, payload any) (any, error) {
			input := payload.(exactJSONDocumentInput)
			received = append(received, input)
			return input, nil
		},
		EncodeContractOutcome: func(request *http.Request, outcome any) (ContractHTTPResponse, error) {
			input := outcome.(exactJSONDocumentInput)
			response, err := EncodeContractJSONForRequest(request, http.StatusOK, outcome, []string{"application/json"}, 0)
			if err != nil || len(input.Filter) == 0 {
				return response, err
			}
			return response, AddContractResponseHeader(&response, "x-filter", nil, ContractResponseValueOptions{Encoding: "json", EncodeValue: func(any) ([]byte, error) { return input.Filter, nil }})
		},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	post := func(t *testing.T, body, filter string) (*httptest.ResponseRecorder, exactJSONDocumentInput) {
		t.Helper()
		received = nil
		request := httptest.NewRequest(http.MethodPost, "/documents", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if filter != "" {
			request.Header.Set("X-Filter", filter)
		}
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || len(received) != 1 {
			t.Fatalf("response = %d %q, handler calls %d", recorder.Code, recorder.Body.String(), len(received))
		}
		return recorder, received[0]
	}
	canonicalHash := func(t *testing.T, document json.RawMessage) [sha256.Size]byte {
		t.Helper()
		canonical, err := contract.MarshalContractValue(document, "json")
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(canonical)
	}

	tests := []struct{ name, document string }{
		{name: "top-level empty array", document: `[]`},
		{name: "top-level empty object", document: `{}`},
		{name: "empty array member", document: `{"kinks":[]}`},
		{name: "null member", document: `{"kinks":null}`},
		{name: "nested empty collections", document: `{"a":[[],{},{"b":[]}],"c":{"d":{}},"e":[[[]]]}`},
	}
	hashes := map[[sha256.Size]byte]string{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, input := post(t, `{"document":`+test.document+`}`, "")
			// Hash what the handler received, as an application storing the
			// document would, before comparing it with what was sent.
			hash := canonicalHash(t, input.Document)
			if previous, exists := hashes[hash]; exists {
				t.Errorf("canonical hash of received %s collides with %s", input.Document, previous)
			}
			hashes[hash] = test.document
			if got := string(input.Document); got != test.document {
				t.Fatalf("handler document = %s, want %s", got, test.document)
			}
			if got, want := recorder.Body.String(), `{"document":`+test.document+`}`; got != want {
				t.Fatalf("response body = %s, want %s", got, want)
			}
		})
	}

	t.Run("JSON-encoded request and response headers", func(t *testing.T) {
		recorder, input := post(t, `{"document":{}}`, `{"ids":[],"tags":{}}`)
		if got, want := string(input.Filter), `{"ids":[],"tags":{}}`; got != want {
			t.Fatalf("handler filter = %s, want %s", got, want)
		}
		if got, want := recorder.Header().Get("X-Filter"), `{"ids":[],"tags":{}}`; got != want {
			t.Fatalf("response header = %s, want %s", got, want)
		}
	})
}

// Typed nullable collections take the same HTTP decoding path: `[]` is a
// present empty list, and only `null` sets Null.
func TestContractTypedNullableListKeepsEmptyDistinctFromNull(t *testing.T) {
	type tagsInput struct {
		Tags contract.Nullable[[]string] `json:"tags"`
	}
	decode := func(t *testing.T, body string) contract.Nullable[[]string] {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/tags", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		input, err := DecodeContractJSON[tagsInput](request)
		if err != nil {
			t.Fatal(err)
		}
		return input.Tags
	}
	if tags := decode(t, `{"tags":[]}`); tags.Null || tags.Value == nil || len(tags.Value) != 0 {
		t.Fatalf("empty list decoded as %#v", tags)
	}
	if tags := decode(t, `{"tags":null}`); !tags.Null || tags.Value != nil {
		t.Fatalf("null decoded as %#v", tags)
	}
	if tags := decode(t, `{"tags":["a"]}`); tags.Null || len(tags.Value) != 1 || tags.Value[0] != "a" {
		t.Fatalf("list decoded as %#v", tags)
	}
}
