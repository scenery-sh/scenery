package runtime

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"scenery.sh/internal/workerabi"
)

func TestWorkerResponseProjectsCanonicalMetadata(t *testing.T) {
	plan := map[string]WorkerResponsePlan{
		"result.ok": {
			Status: http.StatusOK,
			Codec:  "json",
			Media:  []string{"application/json"},
			Body:   WorkerResponseValue{Path: []string{"data"}},
			Headers: []WorkerResponseHeader{
				{Name: "X-Ids", Value: WorkerResponseValue{Path: []string{"data", "ids"}}},
				{Name: "X-Json", Encoding: "json", Value: WorkerResponseValue{Path: []string{"data"}}},
				{Name: "X-Missing", Value: WorkerResponseValue{Path: []string{"missing"}, Optional: true}},
			},
			Cookies: []WorkerResponseCookie{{
				Cookie: ContractResponseCookie{Name: "quote", Secure: true, HTTPOnly: true, SameSite: http.SameSiteLaxMode},
				Value:  WorkerResponseValue{Path: []string{"cookie"}},
			}},
		},
	}
	result := workerabi.Result{Outcome: []byte(`{"kind":"result","name":"ok","value":{"cookie":"two words","data":{"ids":[9007199254740993,2]}}}`)}
	response, err := encodeWorkerResponse(httptest.NewRequest(http.MethodGet, "/", nil), result, plan)
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"ids":[9007199254740993,2]}`
	if response.Status != http.StatusOK || string(response.Body) != body || response.Headers.Get("X-Json") != body {
		t.Fatalf("unexpected response: %+v", response)
	}
	if values := response.Headers.Values("X-Ids"); !slices.Equal(values, []string{"9007199254740993", "2"}) {
		t.Fatalf("X-Ids = %v", values)
	}
	if _, exists := response.Headers["X-Missing"]; exists {
		t.Fatal("missing optional header was emitted")
	}
	if got := response.Headers.Get("Set-Cookie"); got != "quote=two%20words; Path=/; HttpOnly; Secure; SameSite=Lax" {
		t.Fatalf("Set-Cookie = %q", got)
	}
}
