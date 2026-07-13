package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
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
	request, err := json.Marshal(ContractCLIRequest{
		Kind: ContractCLIRequestKind, SchemaRevision: ContractCLIRequestSchemaRevision, SpecRevision: string(spec.CurrentRevision()),
		Producer: machine.Producer{Version: "test", Toolchain: machine.Toolchain{GoVersion: "go-test"}},
		Binding:  "house/binding/process_cli", Input: json.RawMessage(`{"scene_id":"scene-1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
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
	if err := ValidateContractCLIResponse(response, string(spec.CurrentRevision())); err != nil {
		t.Fatal(err)
	}
	if response.Outcome.Name != "processed" || string(response.Outcome.Payload) != `{"scene_id":"scene-1"}` {
		t.Fatalf("response = %#v", response)
	}
}

func TestContractCLISchemaRevisionsMatchCheckedSchemas(t *testing.T) {
	for kind, expected := range map[string]string{
		ContractCLIRequestKind: ContractCLIRequestSchemaRevision, ContractCLIResponseKind: ContractCLIResponseSchemaRevision,
	} {
		encoded, err := os.ReadFile(filepath.Join("..", "docs", "schemas", kind+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		revision, err := spec.SchemaDocumentRevision(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(revision) != expected {
			t.Fatalf("%s schema revision = %s, want %s", kind, revision, expected)
		}
	}
}
