package mcpgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scenery.sh/errs"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpprojection"
)

// projectedFixtureCapability projects the compiled native MCP fixture, so the
// advertised schemas are the ones a real application publishes.
func projectedFixtureCapability(t *testing.T) mcpcontract.Capability {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "mcpprojection", "testdata", "native_graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		WorkspaceRevision string          `json:"workspace_revision"`
		Manifest          *graph.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	projected, err := mcpprojection.ProjectManifest(fixture.Manifest, fixture.WorkspaceRevision, "support")
	if err != nil {
		t.Fatal(err)
	}
	return projected.Capabilities[0]
}

func advertisedSchema(t *testing.T, tools []*mcp.Tool, name string) *jsonschema.Resolved {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		encoded, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatalf("resolve %s output schema: %v", name, err)
		}
		return resolved
	}
	t.Fatalf("tool %s is not advertised", name)
	return nil
}

func structured(t *testing.T, result *mcp.CallToolResult) any {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestGatewayResultsConformToTheAdvertisedSchemas(t *testing.T) {
	local := projectedFixtureCapability(t)
	failing := local
	failing.ID, failing.Name = failing.ID+"#failing", "house__failing_scene"
	manifest := testManifest()
	manifest.Capabilities = []mcpcontract.Capability{failing, local}
	dispatcher := &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{
		local.Name: {Outcome: "processed", Value: json.RawMessage(`{"status":"done"}`)},
	}}
	failingDispatcher := failingToolDispatcher{testDispatcher: dispatcher, failing: failing.Name}
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: manifest.ContractRevision}}
	handler, err := NewHandler(Config{Manifest: manifest, Verify: contexts, Dispatch: failingDispatcher})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session := connectTestClient(t, server.URL, "token")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{local.Name, failing.Name} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"scene_id": "s"}})
		if err != nil {
			t.Fatalf("call %s: %v", name, err)
		}
		if err := advertisedSchema(t, tools.Tools, name).Validate(structured(t, result)); err != nil {
			t.Fatalf("%s result does not match its advertised schema: %v\n%#v", name, err, result.StructuredContent)
		}
		if name == failing.Name {
			problem, _ := structured(t, result).(map[string]any)["problem"].(map[string]any)
			if !result.IsError || problem["code"] != string(errs.InvalidArgument) {
				t.Fatalf("framework failure lost its code: %#v", result.StructuredContent)
			}
		}
	}
}

type failingToolDispatcher struct {
	*testDispatcher
	failing string
}

func (d failingToolDispatcher) CallTool(ctx context.Context, call mcpcontract.ToolCallContext, name string, input json.RawMessage) (mcpcontract.ToolOutcome, error) {
	if name == d.failing {
		return mcpcontract.ToolOutcome{}, &errs.Error{Code: errs.InvalidArgument, Message: "decode MCP tool input: scene_id is required"}
	}
	return d.testDispatcher.CallTool(ctx, call, name, input)
}

func TestFailureProblemReportsTheOutermostTypedCode(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{&errs.Error{Code: errs.ResourceExhausted, Message: "limit"}, "resource_exhausted"},
		{&errs.Error{Code: errs.Internal, Message: "boom"}, mcpcontract.InternalErrorCode},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{transportFailure{code: "invalid_argument", cause: &errs.Error{Code: errs.NotFound}}, "invalid_argument"},
		{os.ErrClosed, mcpcontract.InternalErrorCode},
	} {
		if code, _ := failureProblem(test.err); code != test.want {
			t.Fatalf("failureProblem(%v) = %q, want %q", test.err, code, test.want)
		}
	}
}

type transportFailure struct {
	code  string
	cause error
}

func (f transportFailure) Error() string       { return "transport failure" }
func (f transportFailure) Unwrap() error       { return f.cause }
func (f transportFailure) ProblemCode() string { return f.code }
