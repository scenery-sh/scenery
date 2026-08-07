package mcpfederation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scenery.sh/internal/mcpcontract"
)

func TestFederationAuthFilteringNamesAndLocalPolicy(t *testing.T) {
	remote := newFakeRemote(t, fakeRemoteOptions{AuthScheme: AuthBearer, Secret: "remote-secret"})
	defer remote.Close()
	var calls atomic.Int32
	remote.server.AddTool(&mcp.Tool{Name: "search", Description: "remote hint", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	})
	remote.server.AddTool(&mcp.Tool{Name: "private", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})

	f, err := New(Config{
		Connections: []Connection{{
			Address: "app/mcp_connection/docs", Namespace: "docs", URL: remote.URL(),
			Auth:   Auth{Scheme: AuthBearer, Secret: []byte("remote-secret")},
			Allow:  []string{"search"},
			Policy: ToolPolicy{Approval: mcpcontract.ApprovalAlways, Effect: mcpcontract.Effect{ReadOnly: true}, MaxResultBytes: 4096},
		}},
		LocalToolNames: []string{"local_tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools := f.Tools()
	if len(tools) != 1 || tools[0].Name != "docs__search" {
		t.Fatalf("tools = %#v, want deterministic allow-filtered namespace", tools)
	}
	if tools[0].Approval != mcpcontract.ApprovalAlways || !tools[0].Effect.ReadOnly || tools[0].Effect.Destructive {
		t.Fatalf("local policy was not applied: %#v", tools[0])
	}
	if tools[0].Description != "remote hint" {
		t.Fatalf("description = %q", tools[0].Description)
	}
	result, err := f.Call(context.Background(), "docs__search", json.RawMessage(`{}`))
	if err != nil || result == nil || calls.Load() != 1 {
		t.Fatalf("Call = %#v, %v, calls=%d", result, err, calls.Load())
	}
	if !remote.SawCredential() {
		t.Fatal("remote did not receive the configured bearer credential")
	}
	encoded, _ := json.Marshal(f.Tools())
	if strings.Contains(string(encoded), "remote-secret") {
		t.Fatal("credential leaked through public tool metadata")
	}
}

func TestFederationHeaderAuthAndNoAuthValidation(t *testing.T) {
	remote := newFakeRemote(t, fakeRemoteOptions{AuthScheme: AuthHeader, Header: "X-Remote-Key", Secret: "header-secret"})
	defer remote.Close()
	remote.server.AddTool(testTool("ping"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	f, err := New(Config{Connections: []Connection{{Address: "app/mcp_connection/one", Namespace: "one", URL: remote.URL(), Auth: Auth{Scheme: AuthHeader, Header: "X-Remote-Key", Secret: []byte("header-secret")}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !remote.SawCredential() {
		t.Fatal("remote did not receive configured header credential")
	}
	if _, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "a", URL: remote.URL(), Auth: Auth{Scheme: AuthNone, Secret: []byte("unexpected")}}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("none auth with secret error = %v", err)
	}
	if _, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "a", URL: "http://remote.example.test/mcp"}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("non-loopback HTTP URL error = %v", err)
	}
	if _, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "a", URL: remote.URL(), Allow: []string{"ping"}, Block: []string{"other"}}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("allow+block error = %v", err)
	}
}

func TestFederationCollisionsAndDeterministicOrdering(t *testing.T) {
	remoteA := newFakeRemote(t, fakeRemoteOptions{})
	remoteB := newFakeRemote(t, fakeRemoteOptions{})
	defer remoteA.Close()
	defer remoteB.Close()
	for _, name := range []string{"zeta", "alpha"} {
		remoteA.server.AddTool(testTool(name), emptyTool)
	}
	remoteB.server.AddTool(testTool("other"), emptyTool)
	f, err := New(Config{Connections: []Connection{
		{Address: "a", Namespace: "docs", URL: remoteA.URL()},
		{Address: "b", Namespace: "api", URL: remoteB.URL()},
	}, LocalToolNames: []string{"local"}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := f.Tools()
	want := []string{"api__other", "docs__alpha", "docs__zeta"}
	for i, tool := range got {
		if i >= len(want) || tool.Name != want[i] {
			t.Fatalf("tools = %#v, want names %v", got, want)
		}
	}
	f.Close()
	collision, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: remoteA.URL()}}, LocalToolNames: []string{"docs__alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := collision.Refresh(context.Background()); !errors.Is(err, ErrToolCollision) {
		t.Fatalf("local collision error = %v", err)
	}
	collision.Close()
}

func TestFederationChangeNotificationRefreshesInventory(t *testing.T) {
	remote := newFakeRemote(t, fakeRemoteOptions{})
	defer remote.Close()
	remote.server.AddTool(testTool("first"), emptyTool)
	f, err := New(Config{RefreshEvery: time.Hour, Connections: []Connection{{Address: "a", Namespace: "docs", URL: remote.URL()}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	remote.server.AddTool(testTool("second"), emptyTool)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		names := f.Tools()
		if len(names) == 2 && names[1].Name == "docs__second" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("notification did not refresh tools: %#v", f.Tools())
}

func TestFederationResultLimitTimeoutCancellation(t *testing.T) {
	remote := newFakeRemote(t, fakeRemoteOptions{})
	defer remote.Close()
	remote.server.AddTool(testTool("large"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 128)}}, StructuredContent: map[string]any{}}, nil
	})
	remote.server.AddTool(testTool("slow"), func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	f, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: remote.URL(), CallTimeout: 25 * time.Millisecond, Policy: ToolPolicy{MaxResultBytes: 64}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Call(context.Background(), "docs__large", json.RawMessage(`{}`)); !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("large result error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Call(ctx, "docs__slow", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := f.Call(context.Background(), "docs__slow", json.RawMessage(`{}`)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestFederationRequiredOptionalReadinessAndRateLimit(t *testing.T) {
	remote := newFakeRemote(t, fakeRemoteOptions{})
	remote.server.AddTool(testTool("present"), emptyTool)
	optional := newFakeRemote(t, fakeRemoteOptions{})
	optional.server.AddTool(testTool("optional"), emptyTool)
	optionalURL := optional.URL()
	optional.Close()
	var mu sync.Mutex
	var diagnostics []Diagnostic
	f, err := New(Config{DiagnosticTTL: time.Hour, OnDiagnostic: func(d Diagnostic) { mu.Lock(); defer mu.Unlock(); diagnostics = append(diagnostics, d) }, Connections: []Connection{
		{Address: "required", Namespace: "req", URL: remote.URL(), Required: true},
		{Address: "optional", Namespace: "opt", URL: optionalURL},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.Ready() || len(f.Tools()) != 1 || f.Tools()[0].Name != "req__present" {
		t.Fatalf("readiness/tools = %v %#v", f.Ready(), f.Tools())
	}
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(diagnostics) != 1 || diagnostics[0].Message != "optional MCP connection unavailable" || strings.Contains(fmt.Sprint(diagnostics[0]), optionalURL) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	mu.Unlock()
	remote.http.CloseClientConnections()
	remote.Close()
	if err := f.Refresh(context.Background()); !errors.Is(err, ErrRequiredUnavailable) || f.Ready() {
		t.Fatalf("required outage = %v ready=%v", err, f.Ready())
	}
}

func TestFederationVersionAndCredentialErrorsAreSafe(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var body struct {
				Method string `json:"method"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body.Method == "initialize" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"fake","version":"1"}}}`))
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer remote.Close()
	f, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: remote.URL, Auth: Auth{Scheme: AuthBearer, Secret: []byte("very-secret")}, Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	refreshErr := f.Refresh(context.Background())
	if !errors.Is(refreshErr, ErrRequiredUnavailable) {
		t.Fatalf("version error = %v", refreshErr)
	}
	if strings.Contains(refreshErr.Error(), "very-secret") {
		t.Fatalf("credential leaked in error: %v", refreshErr)
	}
}

func TestFederationCapabilityProjection(t *testing.T) {
	remote := newFakeRemote(t, fakeRemoteOptions{})
	defer remote.Close()
	remote.server.AddTool(&mcp.Tool{Name: "write", Title: "Remote title", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), ReadOnlyHint: true}}, emptyTool)
	f, err := New(Config{Connections: []Connection{{Address: "addr", Namespace: "docs", URL: remote.URL(), Policy: ToolPolicy{Approval: mcpcontract.ApprovalAlways, Effect: mcpcontract.Effect{Destructive: true}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	capabilities := f.Capabilities()
	if len(capabilities) != 1 || capabilities[0].Origin.Kind != "federated" || capabilities[0].Approval != mcpcontract.ApprovalAlways || !capabilities[0].Effect.Destructive {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if len(capabilities[0].InputSchema) == 0 || capabilities[0].Origin.Address != "addr" {
		t.Fatalf("capability projection lost schema/origin: %#v", capabilities[0])
	}
}

func testTool(name string) *mcp.Tool {
	return &mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}
}

func emptyTool(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{StructuredContent: map[string]any{}}, nil
}

func boolPtr(value bool) *bool { return &value }

type fakeRemoteOptions struct {
	AuthScheme AuthScheme
	Header     string
	Secret     string
}

type fakeRemote struct {
	server *mcp.Server
	http   *httptest.Server
	seen   atomic.Bool
}

func newFakeRemote(t *testing.T, options fakeRemoteOptions) *fakeRemote {
	t.Helper()
	impl := &mcp.Implementation{Name: "fake-remote", Version: "1"}
	server := mcp.NewServer(impl, nil)
	remote := &fakeRemote{server: server}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	remote.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if options.AuthScheme == AuthBearer {
			if req.Header.Get("Authorization") != "Bearer "+options.Secret {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			remote.seen.Store(true)
		}
		if options.AuthScheme == AuthHeader {
			if req.Header.Get(options.Header) != options.Secret {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			remote.seen.Store(true)
		}
		handler.ServeHTTP(w, req)
	}))
	return remote
}

func (r *fakeRemote) URL() string { return r.http.URL }

func (r *fakeRemote) Close() { r.http.Close() }

func (r *fakeRemote) SawCredential() bool { return r.seen.Load() }
