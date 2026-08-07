package assistantruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

const testControlToken = "control-token-1"

func testHTTPConfig(serverURL string) HTTPClientConfig {
	return HTTPClientConfig{
		ControlBase:        serverURL,
		ControlToken:       testControlToken,
		AssistantAddress:   "support",
		RuntimeRevision:    "runtime-1",
		CapabilityRevision: "capability-1",
		ControlTimeout:     time.Second,
		StreamTimeout:      time.Second,
		MaxRequestBytes:    1 << 20,
		MaxResponseBytes:   1 << 20,
		MaxEventBytes:      1 << 16,
	}
}

func testControlResponse(request assistantcontrol.Request, typ string) assistantcontrol.Response {
	return assistantcontrol.Response{
		Kind:               assistantcontrol.ResponseKind,
		SchemaRevision:     assistantcontrol.ResponseSchemaRevision,
		Type:               typ,
		RequestID:          request.RequestID,
		AssistantAddress:   request.AssistantAddress,
		RuntimeRevision:    request.RuntimeRevision,
		CapabilityRevision: request.CapabilityRevision,
	}
}

func writeTestControlResponse(w http.ResponseWriter, response assistantcontrol.Response) error {
	data, err := assistantcontrol.MarshalResponse(response)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(data)
	return err
}

func testHTTPMetadata() RequestMetadata {
	return RequestMetadata{
		RequestID:          "request-1",
		AssistantAddress:   "support",
		RuntimeRevision:    "runtime-1",
		CapabilityRevision: "capability-1",
		Principal:          "principal-1",
		ConversationDigest: "digest-1",
	}
}

func TestHTTPClientControlFlowProbeAndCursor(t *testing.T) {
	const sessionID = "session-1"
	const continuation = "continuation-1"
	var sawToken atomic.Bool
	var sawCursor atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(ControlTokenHeader) != testControlToken {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		sawToken.Store(true)
		if r.Method == http.MethodGet {
			if r.URL.Path != "/scenery/v1/control/sessions/"+sessionID+"/events" || r.URL.Query().Get("after") != "4" {
				http.Error(w, "wrong cursor", http.StatusBadRequest)
				return
			}
			sawCursor.Store(true)
			event := assistantcontrol.Event{
				Kind:               assistantcontrol.EventKind,
				SchemaRevision:     assistantcontrol.EventSchemaRevision,
				Type:               assistantcontrol.EventTextDelta,
				AssistantAddress:   "support",
				RuntimeRevision:    "runtime-1",
				CapabilityRevision: "capability-1",
				PrivateSessionID:   sessionID,
				ContinuationToken:  continuation,
				RunID:              "run-1",
				Sequence:           5,
				OccurredAt:         time.Unix(1_700_000_000, 0).UTC(),
				Data:               json.RawMessage(`{"text":"hello"}`),
			}
			data, err := assistantcontrol.MarshalEvent(event)
			if err != nil {
				t.Errorf("marshal event: %v", err)
				return
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = fmt.Fprintf(w, "%s\n", data)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != controlPath {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		request, err := assistantcontrol.ParseRequest(body)
		if err != nil {
			t.Errorf("parse request: %v", err)
			return
		}
		response := testControlResponse(request, "")
		switch request.Type {
		case assistantcontrol.RequestHealth:
			response.Type = assistantcontrol.ResponseHealth
			response.Health = &assistantcontrol.Health{Ready: true, RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1", Status: "ready"}
		case assistantcontrol.RequestInfo:
			response.Type = assistantcontrol.ResponseInfo
			response.Descriptor = &assistantcontrol.RuntimeDescriptor{Kind: assistantcontrol.RuntimeDescriptorKind, SchemaRevision: assistantcontrol.DescriptorSchemaRevision, AssistantAddress: "support", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1", ControlProtocol: assistantcontrol.ControlProtocol, MCPProtocol: assistantcontrol.MCPProtocolVersion}
		case assistantcontrol.RequestCreateConversation:
			response.Type = assistantcontrol.ResponseConversationCreated
			response.PrivateSessionID, response.ContinuationToken, response.RunID = sessionID, continuation, "run-1"
			response.Data = json.RawMessage(`{"conversation_id":"conv-1"}`)
		case assistantcontrol.RequestSendTurn:
			response.Type = assistantcontrol.ResponseTurnAccepted
			response.PrivateSessionID, response.ContinuationToken, response.RunID = sessionID, continuation, "run-2"
		case assistantcontrol.RequestResolveApproval:
			response.Type = assistantcontrol.ResponseApprovalResolved
			response.PrivateSessionID, response.ContinuationToken, response.RunID, response.ApprovalID, response.Decision = sessionID, continuation, request.RunID, request.ApprovalID, request.Decision
		case assistantcontrol.RequestCancelRun:
			response.Type = assistantcontrol.ResponseRunCancelled
			response.PrivateSessionID, response.ContinuationToken, response.RunID = sessionID, continuation, request.RunID
		default:
			t.Errorf("unexpected request type %q", request.Type)
			return
		}
		if err := writeTestControlResponse(w, response); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(testHTTPConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	metadata := testHTTPMetadata()
	created, err := client.StartConversation(context.Background(), StartRequest{RequestMetadata: metadata, RunID: "run-1", Message: "hello"})
	if err != nil || created.PrivateSessionID != sessionID || created.ContinuationToken != continuation || created.ConversationID != "conv-1" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	turn, err := client.SendTurn(context.Background(), TurnRequest{RequestMetadata: metadata, PrivateSessionID: sessionID, ContinuationToken: continuation, RunID: "run-2", Message: "again"})
	if err != nil || turn.RunID != "run-2" {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	approval := ApprovalRequest{RequestMetadata: metadata, PrivateSessionID: sessionID, ContinuationToken: continuation, RunID: turn.RunID, ApprovalID: "approval-1", Decision: assistantcontrol.DecisionAllow}
	if err := client.ResolveApproval(context.Background(), approval); err != nil {
		t.Fatalf("approval: %v", err)
	}
	if err := client.CancelRun(context.Background(), CancelRequest{RequestMetadata: metadata, PrivateSessionID: sessionID, ContinuationToken: continuation, RunID: turn.RunID}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	stream, err := client.StreamEvents(context.Background(), StreamRequest{RequestMetadata: metadata, PrivateSessionID: sessionID, ContinuationToken: continuation, After: 4})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	_ = stream.Close()
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !strings.Contains(string(data), `"sequence":5`) || !sawToken.Load() || !sawCursor.Load() {
		t.Fatalf("stream=%q token=%v cursor=%v", data, sawToken.Load(), sawCursor.Load())
	}
}

func TestHTTPClientRejectsAddressAndRedirect(t *testing.T) {
	for _, raw := range []string{"", "https://127.0.0.1:1", "http://example.com:1234", "http://127.0.0.1:1/private", "http://127.0.0.1:1?x=1"} {
		if _, err := NewHTTPClient(HTTPClientConfig{ControlBase: raw, ControlToken: "x", AssistantAddress: "support", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"}); !errors.Is(err, ErrInvalidControlAddress) {
			t.Errorf("address %q err=%v", raw, err)
		}
	}
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer redirectTarget.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewHTTPClient(testHTTPConfig(redirect.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.StartConversation(context.Background(), StartRequest{RequestMetadata: testHTTPMetadata(), Message: "hello"})
	if !errors.Is(err, ErrRedirectRejected) {
		t.Fatalf("redirect err=%v", err)
	}
}

func TestHTTPClientMismatchMalformedAndOversize(t *testing.T) {
	tests := []struct {
		name string
		body func(assistantcontrol.Request) []byte
		want error
	}{
		{name: "malformed", body: func(assistantcontrol.Request) []byte { return []byte(`{"unexpected":true}`) }, want: ErrMalformedResponse},
		{name: "oversize", body: func(assistantcontrol.Request) []byte { return []byte(strings.Repeat("x", 128)) }, want: ErrResponseTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				request, _ := assistantcontrol.ParseRequest(body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(test.body(request))
			}))
			defer server.Close()
			config := testHTTPConfig(server.URL)
			config.MaxResponseBytes = 64
			config.MaxEventBytes = 64
			client, err := NewHTTPClient(config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.Health(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request, _ := assistantcontrol.ParseRequest(body)
		response := testControlResponse(request, assistantcontrol.ResponseHealth)
		response.RuntimeRevision = "runtime-other"
		response.Health = &assistantcontrol.Health{Ready: true, RuntimeRevision: "runtime-other", CapabilityRevision: "capability-1", Status: "ready"}
		_ = writeTestControlResponse(w, response)
	}))
	defer server.Close()
	client, err := NewHTTPClient(testHTTPConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Health(context.Background()); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("revision mismatch err=%v", err)
	}
}

func TestHTTPClientMalformedAndOversizeEvents(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{name: "malformed", body: "{bad}\n", want: ErrMalformedEvent},
		{name: "oversize", body: strings.Repeat("x", 128) + "\n", want: ErrResponseTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			config := testHTTPConfig(server.URL)
			config.MaxEventBytes = 64
			client, err := NewHTTPClient(config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			stream, err := client.StreamEvents(context.Background(), StreamRequest{RequestMetadata: testHTTPMetadata(), PrivateSessionID: "session-1", ContinuationToken: "continuation-1"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(stream)
			_ = stream.Close()
			if !errors.Is(err, test.want) {
				t.Fatalf("stream err=%v want=%v", err, test.want)
			}
		})
	}
}

func TestHTTPClientCancellationAndClose(t *testing.T) {
	started := make(chan struct{})
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
		close(serverDone)
	}))
	defer server.Close()
	client, err := NewHTTPClient(testHTTPConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.StreamEvents(ctx, StreamRequest{RequestMetadata: testHTTPMetadata(), PrivateSessionID: "session-1", ContinuationToken: "continuation-1"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	result := make(chan error, 1)
	go func() {
		_, readErr := io.ReadAll(stream)
		result <- readErr
	}()
	cancel()
	select {
	case readErr := <-result:
		if !errors.Is(readErr, context.Canceled) {
			t.Fatalf("cancellation err=%v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("body was not closed after cancellation")
	}
}

func TestHTTPClientNoProxyTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "{}") }))
	defer server.Close()
	client, err := NewHTTPClient(testHTTPConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("transport=%T proxyConfigured=%v", client.httpClient.Transport, transport != nil && transport.Proxy != nil)
	}
}

func TestHTTPClientRejectsUnboundedConfigAndMismatchedMetadata(t *testing.T) {
	base := "http://127.0.0.1:1234"
	for name, mutate := range map[string]func(*HTTPClientConfig){
		"control timeout": func(config *HTTPClientConfig) { config.ControlTimeout = maxControlTimeout + time.Nanosecond },
		"stream timeout":  func(config *HTTPClientConfig) { config.StreamTimeout = maxStreamTimeout + time.Nanosecond },
		"request bytes":   func(config *HTTPClientConfig) { config.MaxRequestBytes = maxRequestBytes + 1 },
		"response bytes":  func(config *HTTPClientConfig) { config.MaxResponseBytes = maxResponseBytes + 1 },
		"event bytes":     func(config *HTTPClientConfig) { config.MaxEventBytes = maxEventBytes + 1 },
		"negative limit":  func(config *HTTPClientConfig) { config.MaxEventBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			config := testHTTPConfig(base)
			mutate(&config)
			if _, err := NewHTTPClient(config); !errors.Is(err, ErrInvalidClientConfig) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("mismatched metadata reached helper")
	}))
	defer server.Close()
	client, err := NewHTTPClient(testHTTPConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	metadata := testHTTPMetadata()
	metadata.RuntimeRevision = "stale-runtime"
	_, err = client.StartConversation(context.Background(), StartRequest{RequestMetadata: metadata, Message: "hello"})
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("metadata mismatch err=%v", err)
	}
}

func TestHTTPClientCloseCancelsBlockedControlRequest(t *testing.T) {
	started := make(chan struct{})
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(serverDone)
	}))
	defer server.Close()
	client, err := NewHTTPClient(testHTTPConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, requestErr := client.StartConversation(context.Background(), StartRequest{RequestMetadata: testHTTPMetadata(), Message: "hello"})
		result <- requestErr
	}()
	<-started
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case requestErr := <-result:
		if !errors.Is(requestErr, ErrUnavailable) && !errors.Is(requestErr, ErrStopped) {
			t.Fatalf("close request err=%v", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked control request did not stop")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("blocked control request body was not canceled")
	}
}
