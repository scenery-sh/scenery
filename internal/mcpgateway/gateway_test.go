package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scenery.sh/internal/mcpcontract"
)

type testDispatcher struct {
	mu        sync.Mutex
	calls     []dispatchCall
	byName    map[string]mcpcontract.ToolOutcome
	block     chan struct{}
	cancelled chan struct{}
}

type testFederation struct {
	mu           sync.Mutex
	ready        bool
	capabilities []mcpcontract.Capability
	outcome      mcpcontract.ToolOutcome
	err          error
	block        chan struct{}
	calls        []dispatchCall
}

func (f *testFederation) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *testFederation) Capabilities() []mcpcontract.Capability {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mcpcontract.Capability(nil), f.capabilities...)
}

func (f *testFederation) CallTool(ctx context.Context, call mcpcontract.ToolCallContext, name string, input json.RawMessage) (mcpcontract.ToolOutcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, dispatchCall{Principal: call.Principal, Name: name, Input: append(json.RawMessage(nil), input...), Context: call})
	block := f.block
	outcome, err := f.outcome, f.err
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return mcpcontract.ToolOutcome{}, ctx.Err()
		}
	}
	return outcome, err
}

type dispatchCall struct {
	Principal string
	Name      string
	Input     json.RawMessage
	Context   mcpcontract.ToolCallContext
}

func (d *testDispatcher) CallTool(ctx context.Context, call mcpcontract.ToolCallContext, name string, input json.RawMessage) (mcpcontract.ToolOutcome, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dispatchCall{Principal: call.Principal, Name: name, Input: append(json.RawMessage(nil), input...), Context: call})
	d.mu.Unlock()
	if d.block != nil && name == "slow" {
		select {
		case <-d.block:
		case <-ctx.Done():
			if d.cancelled != nil {
				closeOnce(d.cancelled)
			}
			return mcpcontract.ToolOutcome{}, ctx.Err()
		}
	}
	if outcome, ok := d.byName[name]; ok {
		return outcome, nil
	}
	return mcpcontract.ToolOutcome{Outcome: "completed", Value: json.RawMessage(`{"ok":true}`)}, nil
}

type testAuthorizer struct{}

func (testAuthorizer) Visible(_ context.Context, principal string, capability mcpcontract.Capability) bool {
	return principal == "alice" || capability.Name == "read"
}

type testDurable struct {
	mu     sync.Mutex
	status int
	cancel int
}

func (d *testDurable) Status(_ context.Context, _ mcpcontract.ToolCallContext, id string) (json.RawMessage, error) {
	d.mu.Lock()
	d.status++
	d.mu.Unlock()
	return json.RawMessage(`{"execution_id":"` + id + `","state":"running"}`), nil
}

func (d *testDurable) Cancel(_ context.Context, _ mcpcontract.ToolCallContext, id string) (json.RawMessage, error) {
	d.mu.Lock()
	d.cancel++
	d.mu.Unlock()
	return json.RawMessage(`{"execution_id":"` + id + `","state":"cancelled"}`), nil
}

type assertionTransport struct {
	base  http.RoundTripper
	token string
}

func (t assertionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(AssertionHeader, t.token)
	return t.base.RoundTrip(clone)
}

type requestIDAssertionTransport struct {
	base      http.RoundTripper
	token     string
	requestID string
}

func (t requestIDAssertionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(AssertionHeader, t.token)
	clone.Header.Set("X-Request-ID", t.requestID)
	return t.base.RoundTrip(clone)
}

type mutableAssertionTransport struct {
	base  http.RoundTripper
	mu    sync.RWMutex
	token string
}

func (t *mutableAssertionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	t.mu.RLock()
	token := t.token
	t.mu.RUnlock()
	clone.Header.Set(AssertionHeader, token)
	return t.base.RoundTrip(clone)
}

func (t *mutableAssertionTransport) SetToken(token string) {
	t.mu.Lock()
	t.token = token
	t.mu.Unlock()
}

func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func testManifest() mcpcontract.Manifest {
	objectSchema := json.RawMessage(`{"type":"object"}`)
	limits := mcpcontract.Limits{MaxInputBytes: mcpcontract.MaximumInputBytes, MaxResultBytes: mcpcontract.MaximumResultBytes}
	capability := func(id, name string, durable bool) mcpcontract.Capability {
		return mcpcontract.Capability{
			ID: id, Name: name, Title: name, Description: name,
			InputSchema: objectSchema, OutputSchema: objectSchema,
			OperationAddress: "operation/" + name, ExecutionAddress: "execution/" + name,
			Origin: mcpcontract.Origin{Kind: "local", Address: "local/app"},
			Limits: limits, Effect: mcpcontract.Effect{ReadOnly: name == "read"}, Approval: mcpcontract.ApprovalNever,
			Durable: durable,
		}
	}
	return mcpcontract.Manifest{
		Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision,
		ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "rev-1",
		Capabilities: []mcpcontract.Capability{
			capability("cap-durable", "enqueue", true), capability("cap-read", "read", false), capability("cap-slow", "slow", false), capability("cap-write", "write", false),
		},
	}
}

func federatedCapability(name string) mcpcontract.Capability {
	return mcpcontract.Capability{
		ID:               "remote/docs/" + name,
		Name:             name,
		Title:            "remote " + name,
		Description:      "remote tool",
		InputSchema:      json.RawMessage(`{"type":"object"}`),
		OutputSchema:     json.RawMessage(`{"type":"object"}`),
		OperationAddress: "mcp/federated/operation/" + name,
		ExecutionAddress: "mcp/federated/execution/" + name,
		Origin:           mcpcontract.Origin{Kind: "federated", Address: "app/mcp_connection/docs", Namespace: "docs"},
		Limits:           mcpcontract.Limits{MaxInputBytes: mcpcontract.MaximumInputBytes, MaxResultBytes: mcpcontract.MaximumResultBytes},
		Effect:           mcpcontract.Effect{ReadOnly: true},
		Approval:         mcpcontract.ApprovalAlways,
	}
}

func connectTestClient(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-test-client", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: assertionTransport{base: http.DefaultTransport, token: token}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestGatewayClientRoundTrip(t *testing.T) {
	dispatcher := &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{
		"read":    {Outcome: "completed", Value: json.RawMessage(`{"value":"ok"}`)},
		"write":   {Outcome: "completed", Value: json.RawMessage(`{"written":true}`)},
		"enqueue": {Outcome: "accepted", Receipt: &mcpcontract.DurableReceipt{DurableIdentity: "task", ExecutionID: "run-1", AcceptedRevision: "rev-1"}},
	}}
	durable := &testDurable{}
	contexts := map[string]mcpcontract.ToolCallContext{
		"alice-token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1", RequestID: "req-alice"},
	}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: StaticAssertionVerifier(contexts), Dispatch: dispatcher, Authorizer: testAuthorizer{}, Durable: durable})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session := connectTestClient(t, server.URL, "alice-token")
	if result := session.InitializeResult(); result == nil || result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("negotiated protocol = %#v, want %q", result, ProtocolVersion)
	}

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 6 { // four generated tools plus framework status/cancel
		t.Fatalf("tool count = %d, want 6", len(tools.Tools))
	}

	for _, test := range []struct {
		name string
		want string
	}{
		{"read", `"outcome":"completed"`},
		{"write", `"written":true`},
		{"enqueue", `"execution_id":"run-1"`},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: test.name, Arguments: map[string]any{"x": 1}})
		if err != nil {
			t.Fatalf("call %s: %v", test.name, err)
		}
		payload, _ := json.Marshal(result.StructuredContent)
		if !strings.Contains(string(payload), test.want) {
			t.Fatalf("call %s payload = %s, want %s", test.name, payload, test.want)
		}
		if len(result.Content) != 1 {
			t.Fatalf("call %s content count = %d, want 1", test.name, len(result.Content))
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("call %s content type = %T, want text", test.name, result.Content[0])
		}
		var expected mcpcontract.ToolOutcome
		switch test.name {
		case "read":
			expected = mcpcontract.ToolOutcome{Outcome: "completed", Value: json.RawMessage(`{"value":"ok"}`)}
		case "write":
			expected = mcpcontract.ToolOutcome{Outcome: "completed", Value: json.RawMessage(`{"written":true}`)}
		case "enqueue":
			expected = mcpcontract.ToolOutcome{Outcome: "accepted", Receipt: &mcpcontract.DurableReceipt{DurableIdentity: "task", ExecutionID: "run-1", AcceptedRevision: "rev-1"}}
		}
		expectedBytes, err := mcpcontract.MarshalOutcome(expected)
		if err != nil || text.Text != string(expectedBytes) {
			t.Fatalf("call %s text = %q, want canonical %q (err=%v)", test.name, text.Text, expectedBytes, err)
		}
	}

	problem := mcpcontract.ToolOutcome{Outcome: "rejected", Problem: json.RawMessage(`{"code":"declared","message":"nope"}`)}
	dispatcher.mu.Lock()
	dispatcher.byName["read"] = problem
	dispatcher.mu.Unlock()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "read"})
	if err != nil || !result.IsError {
		t.Fatalf("declared error result = %#v, err=%v", result, err)
	}
	problemBytes, _ := mcpcontract.MarshalOutcome(problem)
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || text.Text != string(problemBytes) {
		t.Fatalf("declared error text = %#v, want canonical %q", result.Content, problemBytes)
	}

	status, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpcontract.StatusToolName, Arguments: map[string]any{"execution_id": "run-1"}})
	if err != nil || !strings.Contains(string(mustJSON(status.StructuredContent)), "running") {
		t.Fatalf("status result = %#v, err=%v", status, err)
	}
	cancel, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpcontract.CancelToolName, Arguments: map[string]any{"execution_id": "run-1"}})
	if err != nil || !strings.Contains(string(mustJSON(cancel.StructuredContent)), "cancelled") {
		t.Fatalf("cancel result = %#v, err=%v", cancel, err)
	}
	durable.mu.Lock()
	statusCalls, cancelCalls := durable.status, durable.cancel
	durable.mu.Unlock()
	if statusCalls != 1 || cancelCalls != 1 {
		t.Fatalf("durable calls status=%d cancel=%d", statusCalls, cancelCalls)
	}
}

func TestGatewayFiltersFrameworkToolsAndEnforcesResultLimit(t *testing.T) {
	manifest := testManifest()
	for index := range manifest.Capabilities {
		if manifest.Capabilities[index].Name == "write" {
			manifest.Capabilities[index].Limits.MaxResultBytes = 16
		}
	}
	dispatcher := &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{
		"write": {Outcome: "completed", Value: json.RawMessage(`{"large":"payload"}`)},
	}}
	durable := &testDurable{}
	contexts := StaticAssertionVerifier{
		"alice-token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"},
		"bob-token":   {Principal: "bob", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"},
	}
	handler, err := NewHandler(Config{Manifest: manifest, Verify: contexts, Dispatch: dispatcher, Authorizer: testAuthorizer{}, Durable: durable})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	alice := connectTestClient(t, server.URL, "alice-token")
	result, err := alice.CallTool(context.Background(), &mcp.CallToolParams{Name: "write"})
	if err != nil || !result.IsError {
		t.Fatalf("oversized result = %#v, err=%v", result, err)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || !strings.Contains(text.Text, `"outcome":"failed"`) {
		t.Fatalf("oversized result text = %#v", result.Content)
	}

	bob := connectTestClient(t, server.URL, "bob-token")
	tools, err := bob.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "read" {
		t.Fatalf("bob tools = %#v, want only read", tools.Tools)
	}
	if _, err := bob.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpcontract.StatusToolName, Arguments: map[string]any{"execution_id": "run-1"}}); err == nil {
		t.Fatal("bob status call unexpectedly succeeded")
	}
	dispatcher.mu.Lock()
	callsBefore := len(dispatcher.calls)
	dispatcher.mu.Unlock()
	if _, err := bob.CallTool(context.Background(), &mcp.CallToolParams{Name: "write"}); err == nil {
		t.Fatal("bob hidden write call unexpectedly succeeded")
	}
	dispatcher.mu.Lock()
	hiddenWriteReached := false
	for _, call := range dispatcher.calls[callsBefore:] {
		if call.Principal == "bob" && call.Name == "write" {
			hiddenWriteReached = true
			break
		}
	}
	dispatcher.mu.Unlock()
	if hiddenWriteReached {
		t.Fatal("bob hidden write reached dispatcher")
	}

	mutable := &mutableAssertionTransport{base: http.DefaultTransport, token: "alice-token"}
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-test-client", Version: "1"}, nil)
	mutableTransport := &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: mutable},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	alice, connectErr := client.Connect(context.Background(), mutableTransport, nil)
	if connectErr != nil {
		t.Fatalf("mutable connect: %v", connectErr)
	}
	t.Cleanup(func() { _ = alice.Close() })
	mutable.SetToken("bob-token")
	if _, err := alice.ListTools(context.Background(), nil); err == nil {
		t.Fatal("session principal swap unexpectedly succeeded")
	}
}

func TestGatewayRejectsUnauthorizedStaleOversizedAndPrivateRequests(t *testing.T) {
	dispatcher := &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}
	contexts := map[string]mcpcontract.ToolCallContext{
		"valid": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"},
		"stale": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "old"},
	}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: StaticAssertionVerifier(contexts), Dispatch: dispatcher, MaxBodyBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	request := func(token string, host string, origin string, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(AssertionHeader, token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	protocolRequest := func(body, version string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(AssertionHeader, "valid")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if version != "" {
			req.Header.Set("Mcp-Protocol-Version", version)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	for _, test := range []struct {
		name   string
		token  string
		host   string
		origin string
		body   string
		status int
	}{
		{"missing assertion", "missing", "127.0.0.1", "", `{}`, http.StatusUnauthorized},
		{"stale revision", "stale", "127.0.0.1", "", `{}`, http.StatusConflict},
		{"non-loopback host", "valid", "evil.example", "", `{}`, http.StatusForbidden},
		{"non-loopback origin", "valid", "127.0.0.1", "https://evil.example", `{}`, http.StatusForbidden},
		{"oversized body", "valid", "127.0.0.1", "", strings.Repeat("x", 129), http.StatusRequestEntityTooLarge},
	} {
		response := request(test.token, test.host, test.origin, test.body)
		_ = response.Body.Close()
		if response.StatusCode != test.status {
			t.Errorf("%s status=%d, want %d", test.name, response.StatusCode, test.status)
		}
	}
	for _, test := range []struct {
		name    string
		body    string
		version string
	}{
		{"unsupported initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, ""},
		{"unsupported header", `{}`, "2025-06-18"},
	} {
		response := protocolRequest(test.body, test.version)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s status=%d, want %d", test.name, response.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestGatewayPropagatesCancellationAndCurrentAssertion(t *testing.T) {
	block := make(chan struct{})
	cancelled := make(chan struct{})
	dispatcher := &testDispatcher{block: block, cancelled: cancelled, byName: map[string]mcpcontract.ToolOutcome{}}
	contexts := map[string]mcpcontract.ToolCallContext{
		"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1", RequestID: "current-request", IdempotencyKey: "dedupe"},
	}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: StaticAssertionVerifier(contexts), Dispatch: dispatcher, Authorizer: testAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session := connectTestClient(t, server.URL, "token")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "slow"})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not observe cancellation")
	}

	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.calls) == 0 || !strings.HasPrefix(dispatcher.calls[len(dispatcher.calls)-1].Context.RequestID, "mcp-") || dispatcher.calls[len(dispatcher.calls)-1].Context.RequestID == "current-request" || dispatcher.calls[len(dispatcher.calls)-1].Context.IdempotencyKey != "dedupe" {
		t.Fatalf("current assertion context not propagated: %+v", dispatcher.calls)
	}
}

func TestGatewayAllocatesLoopbackListener(t *testing.T) {
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"}}
	gateway, err := New(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gateway.URL, "http://127.0.0.1:") {
		t.Fatalf("gateway URL = %q, want loopback", gateway.URL)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- gateway.Serve(context.Background()) }()
	session := connectTestClient(t, gateway.URL, "token")
	if _, err := session.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("list tools through allocated listener: %v", err)
	}
	if err := gateway.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("gateway serve did not stop")
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func TestSignAndVerifyAssertion(t *testing.T) {
	now := time.Unix(1000, 0)
	claims := AssertionClaims{Audience: "scenery", AssistantAddress: "assistant/support", Principal: "alice", ConversationDigest: "conversation", CapabilityRevision: "rev-1", ExpiresAt: now.Add(time.Minute).Unix(), Nonce: "n"}
	token, err := SignAssertion([]byte("secret"), claims)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1", nil)
	req.Header.Set(AssertionHeader, token)
	call, err := (HMACAssertionVerifier{Secret: []byte("secret"), Audience: "scenery", Now: func() time.Time { return now }}).Verify(context.Background(), req)
	if err != nil || call.Principal != "alice" {
		t.Fatalf("verify call=%+v err=%v", call, err)
	}
	if _, err := (HMACAssertionVerifier{Secret: []byte("wrong"), Audience: "scenery", Now: func() time.Time { return now }}).Verify(context.Background(), req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong secret error=%v", err)
	}
	for _, field := range []string{"audience", "assistant", "conversation", "principal", "revision", "nonce"} {
		missing := claims
		switch field {
		case "audience":
			missing.Audience = ""
		case "assistant":
			missing.AssistantAddress = ""
		case "conversation":
			missing.ConversationDigest = ""
		case "principal":
			missing.Principal = ""
		case "revision":
			missing.CapabilityRevision = ""
		case "nonce":
			missing.Nonce = ""
		}
		if _, err := SignAssertion([]byte("secret"), missing); err == nil {
			t.Errorf("missing %s claims unexpectedly signed", field)
		}
	}
}

func TestGatewayFederationMergesToolsAndPropagatesAssertionContext(t *testing.T) {
	remoteCapability := federatedCapability("docs__search")
	federation := &testFederation{
		ready:        true,
		capabilities: []mcpcontract.Capability{remoteCapability},
		outcome:      mcpcontract.ToolOutcome{Outcome: "processed", Value: json.RawMessage(`{"remote":true}`)},
	}
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1", RequestID: "req-1"}}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}, Durable: &testDurable{}, Federation: federation})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session := connectTestClient(t, server.URL, "token")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	wantNames := []string{"docs__search", "enqueue", "read", mcpcontract.CancelToolName, mcpcontract.StatusToolName, "slow", "write"}
	if len(tools.Tools) != len(wantNames) {
		t.Fatalf("tool count=%d names=%#v", len(tools.Tools), tools.Tools)
	}
	for index, want := range wantNames {
		if tools.Tools[index].Name != want {
			t.Fatalf("tool[%d]=%q, want %q", index, tools.Tools[index].Name, want)
		}
		if tools.Tools[index].Annotations == nil && want != mcpcontract.CancelToolName && want != mcpcontract.StatusToolName {
			t.Fatalf("tool[%d]=%q has no policy annotations", index, want)
		}
	}
	remote := tools.Tools[0]
	if remote.Title != remoteCapability.Title || remote.Description != remoteCapability.Description || remote.InputSchema == nil {
		t.Fatalf("remote metadata was not projected: %#v", remote)
	}
	if remote.Annotations == nil || !remote.Annotations.ReadOnlyHint || remote.Annotations.DestructiveHint == nil || *remote.Annotations.DestructiveHint || remote.Annotations.IdempotentHint || remote.Annotations.OpenWorldHint == nil || *remote.Annotations.OpenWorldHint {
		t.Fatalf("remote policy annotations=%+v", remote.Annotations)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "read" && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint) {
			t.Fatalf("local read policy annotation=%+v", tool.Annotations)
		}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: remoteCapability.Name, Arguments: map[string]any{"query": "scenery"}})
	if err != nil || result.IsError {
		t.Fatalf("remote call result=%#v err=%v", result, err)
	}
	federation.mu.Lock()
	if len(federation.calls) != 1 {
		t.Fatalf("federation calls=%d", len(federation.calls))
	}
	call := federation.calls[0]
	federation.mu.Unlock()
	if call.Name != remoteCapability.Name || call.Principal != "alice" || !strings.HasPrefix(call.Context.RequestID, "mcp-") || call.Context.RequestID == "req-1" || string(call.Input) != `{"query":"scenery"}` {
		t.Fatalf("federated call context=%+v input=%s", call.Context, call.Input)
	}
	if remote.Description == "" || strings.Contains(remote.Description, "remote-secret") {
		t.Fatal("federated metadata leaked a credential")
	}
}

func TestGatewayRejectsSamePrincipalSessionContextSwap(t *testing.T) {
	contexts := StaticAssertionVerifier{
		"base":         {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"},
		"assistant":    {Principal: "alice", AssistantAddress: "assistant/other", ConversationDigest: "conversation", CapabilityRevision: "rev-1"},
		"conversation": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "other-conversation", CapabilityRevision: "rev-1"},
		"revision":     {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-2"},
	}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "assistant address", token: "assistant"},
		{name: "conversation digest", token: "conversation"},
		{name: "capability revision", token: "revision"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &mutableAssertionTransport{base: http.DefaultTransport, token: "base"}
			client := mcp.NewClient(&mcp.Implementation{Name: "gateway-test-client", Version: "1"}, nil)
			mcpTransport := &mcp.StreamableClientTransport{
				Endpoint:             server.URL,
				HTTPClient:           &http.Client{Transport: transport},
				DisableStandaloneSSE: true,
				MaxRetries:           -1,
			}
			session, err := client.Connect(context.Background(), mcpTransport, nil)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer session.Close()
			transport.SetToken(test.token)
			if _, err := session.ListTools(context.Background(), nil); err == nil {
				t.Fatal("same-principal stable context swap unexpectedly succeeded")
			}
		})
	}
}

func TestGatewayMintsRequestIDsForEveryToolInvocation(t *testing.T) {
	dispatcher := &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{
		"read": {Outcome: "completed", Value: json.RawMessage(`{"ok":true}`)},
	}}
	contexts := StaticAssertionVerifier{
		"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1", RequestID: "assertion-request", IdempotencyKey: "dedupe"},
	}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-test-client", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: requestIDAssertionTransport{base: http.DefaultTransport, token: "token", requestID: "client-request"}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()
	for range 2 {
		if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "read"}); err != nil {
			t.Fatalf("call read: %v", err)
		}
	}
	dispatcher.mu.Lock()
	calls := append([]dispatchCall(nil), dispatcher.calls...)
	dispatcher.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("dispatcher calls = %d, want 2", len(calls))
	}
	first, second := calls[0].Context, calls[1].Context
	if !strings.HasPrefix(first.RequestID, "mcp-") || !strings.HasPrefix(second.RequestID, "mcp-") || first.RequestID == second.RequestID {
		t.Fatalf("request IDs = %q, %q, want distinct gateway IDs", first.RequestID, second.RequestID)
	}
	if first.RequestID == "assertion-request" || first.RequestID == "client-request" || second.RequestID == "assertion-request" || second.RequestID == "client-request" {
		t.Fatalf("client/assertion request ID leaked into dispatch: %q, %q", first.RequestID, second.RequestID)
	}
	if first.IdempotencyKey != "dedupe" || second.IdempotencyKey != "dedupe" {
		t.Fatalf("trusted assertion idempotency key changed: %q, %q", first.IdempotencyKey, second.IdempotencyKey)
	}
}

func TestGatewayFederationOptionalOmissionAndRequiredOutage(t *testing.T) {
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"}}
	optional := &testFederation{ready: true}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}, Federation: optional})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session := connectTestClient(t, server.URL, "token")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("optional-only list: %v", err)
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "docs__") {
			t.Fatalf("unavailable optional tool was exposed: %q", tool.Name)
		}
	}

	required := &testFederation{ready: false, capabilities: []mcpcontract.Capability{federatedCapability("docs__search")}}
	requiredHandler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}, Federation: required})
	if err != nil {
		t.Fatal(err)
	}
	requiredServer := httptest.NewServer(requiredHandler)
	defer requiredServer.Close()
	requiredSession := connectTestClient(t, requiredServer.URL, "token")
	if _, err := requiredSession.ListTools(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "federated") {
		t.Fatalf("required outage list error=%v, want typed federation failure", err)
	}
}

func TestGatewayFederationRejectsLocalAndFrameworkCollisions(t *testing.T) {
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"}}
	for _, name := range []string{"read", mcpcontract.StatusToolName} {
		federation := &testFederation{ready: true, capabilities: []mcpcontract.Capability{federatedCapability(name)}}
		handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}, Federation: federation})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		session := connectTestClient(t, server.URL, "token")
		_, listErr := session.ListTools(context.Background(), nil)
		_ = session.Close()
		server.Close()
		if listErr == nil || !strings.Contains(listErr.Error(), "collides") {
			t.Fatalf("collision %q list error=%v", name, listErr)
		}
	}
}

func TestGatewayFederationCancellationAndSafeErrorNormalization(t *testing.T) {
	secretError := errors.New("POST https://remote.example.test/mcp Authorization: Bearer super-secret")
	federation := &testFederation{ready: true, capabilities: []mcpcontract.Capability{federatedCapability("docs__slow")}, err: secretError}
	contexts := StaticAssertionVerifier{"token": {Principal: "alice", AssistantAddress: "assistant/support", ConversationDigest: "conversation", CapabilityRevision: "rev-1"}}
	handler, err := NewHandler(Config{Manifest: testManifest(), Verify: contexts, Dispatch: &testDispatcher{byName: map[string]mcpcontract.ToolOutcome{}}, Federation: federation})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	session := connectTestClient(t, server.URL, "token")
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "docs__slow"})
	if err != nil || !result.IsError {
		t.Fatalf("remote error result=%#v err=%v", result, err)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || strings.Contains(text.Text, "super-secret") || strings.Contains(text.Text, "remote.example.test") || !strings.Contains(text.Text, "federated MCP tool call failed") {
		t.Fatalf("unsafe remote error text=%#v", result.Content)
	}

	block := make(chan struct{})
	federation.mu.Lock()
	federation.err = nil
	federation.block = block
	federation.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "docs__slow"})
	if !errors.Is(err, context.DeadlineExceeded) || result != nil {
		t.Fatalf("cancel result=%#v err=%v", result, err)
	}
}
