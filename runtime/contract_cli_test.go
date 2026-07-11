package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestContractCLIRequestInvokesRegisteredBinding(t *testing.T) {
	previous := global
	global = &registry{contractCLIBindings: map[string]ContractCLIBindingRegistration{}}
	t.Cleanup(func() { global = previous })
	if err := RegisterContractCLIBinding(ContractCLIBindingRegistration{
		Address: "house/binding/process_cli", Command: []string{"house", "process"},
		Policy: &ContractHTTPPolicy{BindingAddress: "house/binding/process_cli", AuthorizationStrategy: "public"},
		Invoke: func(_ context.Context, input []byte) (ContractCLIOutcome, error) {
			return ContractCLIOutcome{Kind: "result", Name: "processed", Payload: append([]byte(nil), input...)}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "request.json")
	request := `{"api_version":"scenery.contract-cli-request/v1","binding":"house/binding/process_cli","input":{"scene_id":"scene-1"}}`
	if err := os.WriteFile(requestPath, []byte(request), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := ExecuteContractCLIRequest(requestPath, &output); err != nil {
		t.Fatal(err)
	}
	var response ContractCLIResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Problem != nil || response.Outcome == nil || response.Outcome.Name != "processed" || string(response.Outcome.Payload) != `{"scene_id":"scene-1"}` {
		t.Fatalf("response = %#v", response)
	}
}
