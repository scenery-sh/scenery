package mcpfederation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scenery.sh/internal/mcpcontract"
)

func TestFederationConservativeDefaultPolicyAndMetadataBounds(t *testing.T) {
	remote := &remoteConnection{cfg: Connection{Address: "app/mcp_connection/docs", Namespace: "docs"}}
	longTitle := strings.Repeat("😀", maxToolTitle)
	longDescription := strings.Repeat("é", maxToolDescription)
	tools, err := remote.projectTools([]*mcp.Tool{{
		Name:        "search",
		Title:       longTitle,
		Description: longDescription,
		InputSchema: map[string]any{"type": "object"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	tool := tools[0]
	if tool.Approval != mcpcontract.ApprovalAlways || !tool.Effect.Destructive || !tool.Effect.OpenWorld {
		t.Fatalf("default remote policy is not fail-closed: %#v", tool)
	}
	if len(tool.Title) > maxToolTitle || len(tool.Description) > maxToolDescription || !utf8.ValidString(tool.Title) || !utf8.ValidString(tool.Description) {
		t.Fatalf("metadata bounds/UTF-8 violated: title=%d description=%d", len(tool.Title), len(tool.Description))
	}
	if !strings.HasSuffix(tool.Title, "😀") || !strings.HasSuffix(tool.Description, "é") {
		t.Fatalf("metadata was truncated at a partial rune")
	}
}

func TestFederationConfigRejectsUnsafeHeaderAndBounds(t *testing.T) {
	validURL := "https://remote.example.test/mcp"
	for _, header := range []string{"X-Key\r\nInjected", "X Key", "Mcp-Protocol-Version", "Content-Type"} {
		if _, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: validURL, Auth: Auth{Scheme: AuthHeader, Header: header, Secret: []byte("secret")}}}}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("header %q error = %v, want ErrInvalidConfig", header, err)
		}
	}
	if _, err := New(Config{Connections: []Connection{{Address: "a\nsecret", Namespace: "docs", URL: validURL}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("control address error = %v", err)
	}
	if _, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: validURL, RefreshTTL: 25 * time.Hour}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unbounded refresh TTL error = %v", err)
	}
	if _, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: validURL, Auth: Auth{Scheme: AuthBearer, Secret: bytes.Repeat([]byte("x"), maxAuthSecret+1)}}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("oversized secret error = %v", err)
	}
}

func TestFederationTransportBoundsAndRedirectCredentialIsolation(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Remote-Key") != "" {
			t.Errorf("credential crossed redirect: headers=%v", r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer redirectTarget.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := newHTTPClient(Auth{Scheme: AuthBearer, Secret: []byte("secret")}, 32)
	request, err := http.NewRequest(http.MethodGet, redirect.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status = %d", response.StatusCode)
	}
	transport, ok := client.Transport.(authRoundTripper)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	base, ok := transport.base.(*http.Transport)
	if !ok {
		t.Fatalf("base transport type = %T", transport.base)
	}
	if base.Proxy != nil {
		t.Fatal("ambient proxy was retained")
	}

	exact := &limitedBody{ioReadCloser: io.NopCloser(bytes.NewReader([]byte("{}"))), remaining: 2}
	data, err := io.ReadAll(exact)
	if err != nil || string(data) != "{}" {
		t.Fatalf("exact-limit body = %q, %v", data, err)
	}
	oversized := &limitedBody{ioReadCloser: io.NopCloser(bytes.NewReader([]byte("{}x"))), remaining: 2}
	data, err = io.ReadAll(oversized)
	if !errors.Is(err, ErrResponseTooLarge) || string(data) != "{}" {
		t.Fatalf("oversized body = %q, %v", data, err)
	}
}

func TestFederationCloseCancelsBackgroundRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
	}))
	defer remote.Close()
	defer close(release)
	f, err := New(Config{RefreshEvery: 10 * time.Millisecond, Connections: []Connection{{Address: "a", Namespace: "docs", URL: remote.URL, ConnectTimeout: 10 * time.Minute}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		_ = f.Close()
		t.Fatal("background refresh did not start")
	}
	closed := make(chan struct{})
	go func() {
		_ = f.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Federation.Close did not cancel blocked refresh")
	}
}

func TestHTTPClientShutdownCancelsActiveRoundTrip(t *testing.T) {
	started := make(chan struct{})
	requestDone := make(chan struct{})
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(requestDone)
		case <-release:
		}
	}))
	defer remote.Close()
	defer close(release)

	shutdown := make(chan struct{})
	client := newHTTPClient(Auth{}, maxHTTPResponse, shutdown)
	request, err := http.NewRequest(http.MethodPost, remote.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := client.Do(request)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	close(shutdown)
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel active HTTP request")
	}
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server request context was not canceled")
	}
}

func TestFederationCloseScrubsConnectionCredential(t *testing.T) {
	f, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: "https://remote.example.test/mcp", Auth: Auth{Scheme: AuthBearer, Secret: []byte("secret")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := f.connections[0].cfg.Auth.Secret; got != nil {
		t.Fatalf("connection secret retained after close: %x", got)
	}
}

func TestFederationManualPaginationUsesSDKPages(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "paged", Version: "1"}, &mcp.ServerOptions{PageSize: 1})
	server.AddTool(testTool("alpha"), emptyTool)
	server.AddTool(testTool("beta"), emptyTool)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	remote := httptest.NewServer(handler)
	defer remote.Close()
	f, err := New(Config{Connections: []Connection{{Address: "a", Namespace: "docs", URL: remote.URL}}, MaxTools: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.Tools(); len(got) != 2 || got[0].Name != "docs__alpha" || got[1].Name != "docs__beta" {
		t.Fatalf("paged tools = %#v", got)
	}
}

func TestFederationUnavailableConnectionRetriesOnRefreshCadence(t *testing.T) {
	available := atomic.Bool{}
	diagnostics := make(chan Diagnostic, 4)
	server := mcp.NewServer(&mcp.Implementation{Name: "recovering", Version: "1"}, nil)
	server.AddTool(testTool("recovered"), emptyTool)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	f, err := New(Config{
		RefreshEvery:  10 * time.Millisecond,
		DiagnosticTTL: time.Nanosecond,
		OnDiagnostic: func(d Diagnostic) {
			diagnostics <- d
		},
		Connections: []Connection{{Address: "a", Namespace: "docs", URL: remote.URL, RefreshTTL: time.Hour}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Observe the initial failure plus a cadence-driven retry. Waiting on
	// diagnostics keeps the test synchronized with completed refresh attempts
	// instead of guessing how long the HTTP round trips will take.
	for attempt := 1; attempt <= 2; attempt++ {
		select {
		case diagnostic := <-diagnostics:
			if diagnostic.Address != "a" || diagnostic.Code != "MCP_CONNECTION_UNAVAILABLE" {
				t.Fatalf("retry diagnostic %d = %#v", attempt, diagnostic)
			}
		case <-time.After(time.Second):
			t.Fatalf("refresh retry %d did not complete", attempt)
		}
	}
	available.Store(true)
	probe := time.NewTicker(time.Millisecond)
	defer probe.Stop()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-probe.C:
			if tools := f.Tools(); len(tools) == 1 && tools[0].Name == "docs__recovered" {
				return
			}
		case <-timeout.C:
			t.Fatalf("connection did not recover on retry cadence: %#v", f.Snapshot())
		}
	}
}
