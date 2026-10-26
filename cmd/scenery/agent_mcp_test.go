package main

import (
	"context"
	"encoding/json"
	"fmt"
	"scenery.sh/errs"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDevelopmentMCPAdapterListsToolsAndReportsTypedFailures(t *testing.T) {
	root := t.TempDir()
	writeHarnessTestApp(t, root, "agent_mcp", "")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverSession, err := newDevelopmentMCPServer(root).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"runtime_status", "runtime_wait", "bindings_list", "binding_invoke", "diagnostics", "trace_get"} {
		if !names[name] {
			t.Fatalf("tool %s is missing from %v", name, names)
		}
	}
	diagnostics, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "diagnostics", Arguments: map[string]any{}})
	if err != nil || diagnostics.IsError {
		t.Fatalf("diagnostics = %#v, %v", diagnostics, err)
	}
	if structured, _ := diagnostics.StructuredContent.(map[string]any); structured["valid"] != true || structured["workspace_revision"] == "" {
		t.Fatalf("diagnostics content = %#v", diagnostics.StructuredContent)
	}
	listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "bindings_list", Arguments: map[string]any{}})
	if err != nil || listed.IsError {
		t.Fatalf("bindings_list = %#v, %v", listed, err)
	}
	for name, arguments := range map[string]map[string]any{
		"binding_invoke": {"command": []string{"absent"}},
		"diagnostics":    {"unexpected": true},
	} {
		failed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil || !failed.IsError {
			t.Fatalf("%s failure = %#v, %v", name, failed, err)
		}
		encoded, _ := json.Marshal(failed.StructuredContent)
		var content struct {
			Problem struct {
				Code string `json:"code"`
			} `json:"problem"`
		}
		_ = json.Unmarshal(encoded, &content)
		want := map[string]string{"binding_invoke": "not_found", "diagnostics": "invalid_argument"}[name]
		if content.Problem.Code != want {
			t.Fatalf("%s problem = %s, want %s", name, encoded, want)
		}
	}
}

func TestDevelopmentMCPFailurePreservesPlainAndTypedCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("failed_precondition: no runtime"), "failed_precondition"},
		{&codedCLIError{err: fmt.Errorf("permission_denied: refused"), code: 3}, "permission_denied"},
		{fmt.Errorf("wrapped: %w", &errs.Error{Code: errs.NotFound, Message: "absent"}), "not_found"},
		{&errs.Error{Code: errs.Internal, Message: "failure"}, "internal"},
		{fmt.Errorf("ordinary failure"), "internal_error"},
	} {
		result := developmentMCPFailure(tc.err)
		problem := result.StructuredContent.(map[string]any)["problem"].(map[string]any)
		if problem["code"] != tc.want {
			t.Fatalf("%v: code = %v, want %s", tc.err, problem["code"], tc.want)
		}
	}
}
