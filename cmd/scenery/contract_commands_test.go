package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"scenery.sh/internal/contractagent"
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
