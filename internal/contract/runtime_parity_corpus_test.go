package contract

import (
	_ "embed"
	"encoding/json"
	"testing"
)

//go:embed testdata/runtime-parity-v1.json
var runtimeParityCorpus []byte

func TestRuntimeParityV1CorpusStructure(t *testing.T) {
	t.Parallel()

	var corpus struct {
		Version         int `json:"version"`
		HTTPNegotiation []struct {
			Name      string   `json:"name"`
			Accept    []string `json:"accept"`
			Offered   []string `json:"offered"`
			WantMedia string   `json:"want_media"`
			Reject    bool     `json:"reject"`
		} `json:"http_negotiation"`
		TransportErrors []struct {
			Name         string `json:"name"`
			Status       int    `json:"status"`
			Body         string `json:"body"`
			WantCode     string `json:"want_code"`
			WantDelivery string `json:"want_delivery"`
		} `json:"transport_errors"`
	}
	if err := json.Unmarshal(runtimeParityCorpus, &corpus); err != nil {
		t.Fatalf("decode runtime-parity-v1.json: %v", err)
	}
	if corpus.Version != 1 {
		t.Fatalf("version = %d, want 1", corpus.Version)
	}
	if len(corpus.HTTPNegotiation) == 0 || len(corpus.TransportErrors) == 0 {
		t.Fatalf("corpus sections must be non-empty")
	}
	seen := map[string]bool{}
	for _, tc := range corpus.HTTPNegotiation {
		if tc.Name == "" || seen["http:"+tc.Name] {
			t.Fatalf("invalid or duplicate http_negotiation case %q", tc.Name)
		}
		seen["http:"+tc.Name] = true
		if len(tc.Offered) == 0 {
			t.Fatalf("http_negotiation %q missing offered media types", tc.Name)
		}
		if !tc.Reject && tc.WantMedia == "" {
			t.Fatalf("http_negotiation %q must set want_media or reject", tc.Name)
		}
	}
	for _, tc := range corpus.TransportErrors {
		if tc.Name == "" || seen["err:"+tc.Name] {
			t.Fatalf("invalid or duplicate transport_errors case %q", tc.Name)
		}
		seen["err:"+tc.Name] = true
		if tc.Status < 400 || tc.WantCode == "" || (tc.WantDelivery != "not_sent" && tc.WantDelivery != "unknown") {
			t.Fatalf("invalid transport_errors case %+v", tc)
		}
	}
}
