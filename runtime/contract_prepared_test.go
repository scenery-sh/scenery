package runtime

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"scenery.sh/internal/contract"
)

func TestContractPreparedOutcomeValueUnwraps(t *testing.T) {
	t.Parallel()
	outcome, envelope := ContractPreparedOutcomeValue(NewContractPreparedOutcome("typed", []byte(`{}`)))
	if outcome != "typed" || string(envelope) != "{}" {
		t.Fatalf("unwrapped = %v envelope=%q", outcome, envelope)
	}
	outcome, envelope = ContractPreparedOutcomeValue("plain")
	if outcome != "plain" || envelope != nil {
		t.Fatalf("plain outcome changed: %v envelope=%q", outcome, envelope)
	}
}

func TestEncodeContractPreparedRepresentationReusesEnvelopePayload(t *testing.T) {
	t.Parallel()
	value := json.RawMessage(`{"b":"two","a":"one"}`)
	envelope, err := contract.MarshalContractOutcomeVariant("result", "ok", value, "json")
	if err != nil {
		t.Fatal(err)
	}
	options := ContractResponseOptions{EncodeValue: func(value any) ([]byte, error) {
		return contract.MarshalContractValue(value, "json")
	}}
	request := httptest.NewRequest("GET", "/example", nil)
	prepared, err := EncodeContractPreparedRepresentationWithOptions(request, 200, envelope, value, "json", []string{"application/json"}, options)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := EncodeContractRepresentationWithOptions(request, 200, value, "json", []string{"application/json"}, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared.Body, plain.Body) {
		t.Fatalf("prepared body %q differs from encoded body %q", prepared.Body, plain.Body)
	}
	if prepared.Status != 200 || prepared.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("prepared response metadata: status=%d headers=%v", prepared.Status, prepared.Headers)
	}
	fallback, err := EncodeContractPreparedRepresentationWithOptions(request, 200, nil, value, "json", []string{"application/json"}, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fallback.Body, plain.Body) {
		t.Fatalf("fallback body %q differs from encoded body %q", fallback.Body, plain.Body)
	}
}
