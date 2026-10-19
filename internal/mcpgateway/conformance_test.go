package mcpgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scenery.sh/errs"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpprojection"
)

// projectedFixtureCapability projects the compiled native MCP fixture, so the
// advertised schemas are the ones a real application publishes.
func projectedFixtureCapability(t *testing.T, durable bool) mcpcontract.Capability {
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
	if durable {
		for index := range fixture.Manifest.Resources {
			if fixture.Manifest.Resources[index].Address == "house/execution/process_scene_direct" {
				fixture.Manifest.Resources[index].Spec["mode"] = "durable"
			}
		}
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
	local := projectedFixtureCapability(t, false)
	durable := projectedFixtureCapability(t, true)
	durable.ID, durable.Name = durable.ID+"#durable", "house__enqueue_scene"
	failing := local
	failing.ID, failing.Name = failing.ID+"#failing", "house__failing_scene"
	remote := federatedCapability("docs__render")
	remote.OutputSchema = json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}},"required":["status"],"additionalProperties":false}`)
	remoteResult, err := json.Marshal(&mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{MIMEType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
			&mcp.ResourceLink{URI: "https://docs.example.test/page", Name: "page"},
		},
		StructuredContent: map[string]any{"status": "rendered"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest()
	manifest.Capabilities = []mcpcontract.Capability{durable, failing, local}
	dispatcher := &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{
		local.Name:   {Outcome: "processed", Value: json.RawMessage(`{"status":"done"}`)},
		durable.Name: {Outcome: mcpcontract.AcceptedOutcome, Receipt: &mcpcontract.DurableReceipt{DurableIdentity: durable.ExecutionAddress, ExecutionID: "job-1", AcceptedRevision: "sha256:rev"}},
	}}
	failingDispatcher := failingToolDispatcher{testDispatcher: dispatcher, failing: failing.Name}
	federation := &testFederation{ready: true, capabilities: []mcpcontract.Capability{remote}, outcome: mcpcontract.ToolOutcome{Outcome: "processed", Value: remoteResult}}
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: manifest.ContractRevision}}
	handler, err := NewHandler(Config{Manifest: manifest, Verify: contexts, Dispatch: failingDispatcher, Durable: &testDurable{}, Federation: federation})
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
	for _, name := range []string{local.Name, durable.Name, failing.Name, remote.Name} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"scene_id": "s"}})
		if err != nil {
			t.Fatalf("call %s: %v", name, err)
		}
		if err := advertisedSchema(t, tools.Tools, name).Validate(structured(t, result)); err != nil {
			t.Fatalf("%s result does not match its advertised schema: %v\n%#v", name, err, result.StructuredContent)
		}
		switch name {
		case failing.Name:
			problem, _ := structured(t, result).(map[string]any)["problem"].(map[string]any)
			if !result.IsError || problem["code"] != string(errs.InvalidArgument) {
				t.Fatalf("framework failure lost its code: %#v", result.StructuredContent)
			}
		case remote.Name:
			if len(result.Content) != 2 {
				t.Fatalf("remote content blocks = %#v", result.Content)
			}
			if image, ok := result.Content[0].(*mcp.ImageContent); !ok || image.MIMEType != "image/png" {
				t.Fatalf("remote image was not relayed as an image block: %#v", result.Content[0])
			}
			if link, ok := result.Content[1].(*mcp.ResourceLink); !ok || link.URI != "https://docs.example.test/page" {
				t.Fatalf("remote resource link was not relayed: %#v", result.Content[1])
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
		{ErrFederationCall, "unavailable"},
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

// finishingDurable reports running until its status has been read finishAfter
// times, then succeeded.
type finishingDurable struct {
	testDurable
	finishAfter int
	reads       int
}

func (d *finishingDurable) Status(_ context.Context, _ mcpcontract.ToolCallContext, id string) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads++
	if d.finishAfter > 0 && d.reads >= d.finishAfter {
		return json.RawMessage(`{"execution_id":"` + id + `","state":"succeeded","outcome":{"done":true}}`), nil
	}
	return json.RawMessage(`{"execution_id":"` + id + `","state":"running"}`), nil
}

func TestGatewayDurableWaitEndsAtAFinalStateOrItsOwnTimeout(t *testing.T) {
	previous := durableWaitPoll
	durableWaitPoll = time.Millisecond
	t.Cleanup(func() { durableWaitPoll = previous })
	durable := &finishingDurable{finishAfter: 3}
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"}}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}, Durable: durable})
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
	schema := advertisedSchema(t, tools.Tools, mcpcontract.WaitToolName)
	call := func(arguments map[string]any) (*mcp.CallToolResult, map[string]any) {
		t.Helper()
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpcontract.WaitToolName, Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		value := structured(t, result)
		if err := schema.Validate(value); err != nil {
			t.Fatalf("wait result does not match its advertised schema: %v\n%#v", err, value)
		}
		return result, value.(map[string]any)
	}
	_, finished := call(map[string]any{"execution_id": "job-1", "timeout_ms": 5000})
	wait := finished["value"].(map[string]any)
	if wait["final"] != true || wait["timed_out"] != false || wait["status"].(map[string]any)["state"] != "succeeded" {
		t.Fatalf("finished wait = %#v", wait)
	}
	durable.mu.Lock()
	durable.finishAfter = 0
	durable.mu.Unlock()
	_, pending := call(map[string]any{"execution_id": "job-2", "timeout_ms": 5})
	wait = pending["value"].(map[string]any)
	if wait["final"] != false || wait["timed_out"] != true || wait["status"].(map[string]any)["state"] != "running" {
		t.Fatalf("timed-out wait = %#v", wait)
	}
	refused, invalid := call(map[string]any{"execution_id": "job-3", "timeout_ms": 999999})
	if !refused.IsError || invalid["problem"].(map[string]any)["code"] != string(errs.InvalidArgument) {
		t.Fatalf("oversized timeout = %#v", invalid)
	}
}
