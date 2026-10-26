package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/contractagent"
	"scenery.sh/internal/graph"
)

func TestContractAgentServerRejectsModelControlledIdentity(t *testing.T) {
	root := t.TempDir()
	writeHarnessTestApp(t, root, "agent-context", "")

	var output bytes.Buffer
	request := `{"jsonrpc":"2.0","id":1,"method":"changes.plan","params":{"base_workspace_revision":"unknown","base_contract_revision":null,"caller":"attacker","capabilities":["root"],"operations":[]}}
`
	if err := runContractAgentServer(strings.NewReader(request), &output, []string{"serve", "--stdio", "--app-root", root}); err != nil {
		t.Fatalf("runContractAgentServer: %v", err)
	}
	var response contractagent.AgentResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", output.String(), err)
	}
	if response.Error == nil || response.Error.Kind != "invalid_request" {
		t.Fatalf("response error = %#v, want invalid_request", response.Error)
	}
	if !strings.Contains(response.Error.Message, "caller") {
		t.Fatalf("response error = %#v, want caller to be rejected", response.Error)
	}
}

func TestContractAgentServerRejectsModelApprovalTokens(t *testing.T) {
	root := t.TempDir()
	writeHarnessTestApp(t, root, "agent-approval", "")

	const secret = "approval-secret"
	var output bytes.Buffer
	request := `{"jsonrpc":"2.0","id":1,"method":"changes.apply","params":{"plan_id":"not-issued","approval_tokens":[{"token":"` + secret + `"}]}}
`
	if err := runContractAgentServer(strings.NewReader(request), &output, []string{"serve", "--stdio", "--app-root", root}); err != nil {
		t.Fatalf("runContractAgentServer: %v", err)
	}
	var response contractagent.AgentResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", output.String(), err)
	}
	if response.Error == nil || response.Error.Kind != "invalid_request" {
		t.Fatalf("response error = %#v, want invalid_request", response.Error)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("approval token leaked through agent response: %q", output.String())
	}
}

func TestContractAgentServerReusesAnUnchangedGraphAndAnswersSchemasStatically(t *testing.T) {
	root := t.TempDir()
	writeHarnessTestApp(t, root, "agent-reuse", "")
	snapshots := newAgentServeSnapshots(root)
	first, err := snapshots.current()
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshots.current()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || snapshots.compiles != 1 {
		t.Fatalf("unchanged sources recompiled: compiles=%d same=%t", snapshots.compiles, first == second)
	}
	path := filepath.Join(root, testAppFilename)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(source, []byte("\n# edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := snapshots.current()
	if err != nil {
		t.Fatal(err)
	}
	if third == second || snapshots.compiles != 2 {
		t.Fatalf("changed sources reused the stale graph: compiles=%d", snapshots.compiles)
	}

	// schema.get is answered from the catalog even without an application graph.
	response := contractagent.NewAgentSession().Handle(nil, contractagent.AgentRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "schema.get", Params: json.RawMessage(`{"kind":"` + graph.DiagnosticCatalog + `"}`)})
	if response.Error != nil || response.Result == nil {
		t.Fatalf("schema.get without a graph = %#v", response)
	}
}
