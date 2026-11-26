package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// The Host describes the admitted HTTP request in the invocation metadata. A
// Go handler reads it through CurrentRequest, as it does in process.
func TestGoWorkerRestoresIngressRequest(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	const operation = "orders/operation/update"
	var observed shared.Request
	if err := RegisterWorkerOperation(operation, func(context.Context, []byte) ([]byte, shared.ByteStream, error) {
		observed = *CurrentRequest()
		return []byte(`{"kind":"result","name":"ok","value":{}}`), shared.ByteStream{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := invokeGoWorker(t.Context(), workerabi.Invocation{
		Service: "orders/service/api", Operation: operation, Mode: "direct", Input: json.RawMessage(`{}`),
		Context: workerabi.InvocationContext{InvocationID: "call", Metadata: json.RawMessage(`{
			"request_type": "api-call", "method": "PUT", "path": "/orders/42/lines/7",
			"path_params": [{"name": "order", "value": "42"}, {"name": "line", "value": "7"}],
			"headers": {"x-correlation-id": ["corr-1"], "Accept-Language": ["cs", "en"]}
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantParams := shared.PathParams{{Name: "order", Value: "42"}, {Name: "line", Value: "7"}}
	if observed.Type != shared.APICall || observed.Method != http.MethodPut || observed.Path != "/orders/42/lines/7" ||
		!slices.Equal(observed.PathParams, wantParams) || observed.PathParams.Get("line") != "7" ||
		observed.Headers.Get("X-Correlation-Id") != "corr-1" || !slices.Equal(observed.Headers.Values("Accept-Language"), []string{"cs", "en"}) {
		t.Fatalf("request observed by the Go worker: %+v", observed)
	}
}

func TestGoWorkerRestoresAPIIdempotencyPayloadAndCapabilityErrors(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	const operation = "jobs/operation/sync"
	var observed shared.Request
	if err := RegisterWorkerOperation(operation, func(ctx context.Context, _ []byte) ([]byte, shared.ByteStream, error) {
		SetWorkerRequestPayload(ctx, map[string]string{"job": "nightly"})
		observed = *CurrentRequest()
		return []byte(`{"kind":"result","name":"ok","value":{}}`), shared.ByteStream{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := invokeGoWorker(t.Context(), workerabi.Invocation{
		Service: "jobs/service/jobs", Operation: operation, Mode: "durable", Input: json.RawMessage(`{}`),
		Context: workerabi.InvocationContext{InvocationID: "call", Metadata: json.RawMessage(`{
			"api":{"exposed":true,"auth_required":true},
			"cron_idempotency_key":"cron-key-1"
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.API == nil || !observed.API.Exposed || !observed.API.AuthRequired || observed.CronIdempotencyKey != "cron-key-1" {
		t.Fatalf("observed API/cron metadata = %+v", observed)
	}
	if payload, ok := observed.Payload.(map[string]string); !ok || payload["job"] != "nightly" {
		t.Fatalf("observed Payload = %#v", observed.Payload)
	}

	directory, err := os.MkdirTemp("", "abi-cap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "cap")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		errs.HTTPError(w, errs.B().Code(errs.PermissionDenied).Msg("reauth needed").Err())
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	transport := workerabi.NewTransport(socket)
	defer transport.CloseIdleConnections()
	ctx := context.WithValue(t.Context(), workerCapabilitiesKey{}, &workerCapabilities{scope: "scope-1", token: "tok", transport: transport})
	_, err = callWorkerCapability(ctx, http.MethodPost, "connections/token", map[string]any{"provider": "google"})
	if errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("capability error = %v (code %s), want permission_denied", err, errs.Code(err))
	}
}

func TestGoWorkerEventProducerPreservesInvocationContextAndState(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	const operation = "events/operation/watch"
	var (
		seenReq        shared.Request
		seenPrincipal  string
		seenInvocation string
		seenScope      string
		seenCancel     bool
	)
	if err := RegisterWorkerEventOperation(operation, func(context.Context, []byte) ([]byte, RawEventProducer, error) {
		return []byte(`{"kind":"result","name":"event","value":null}`), func(ctx context.Context, emit func([]byte, string, string) error) error {
			if req := CurrentRequest(); req != nil {
				seenReq = *req
			}
			seenPrincipal = CurrentAuth().UID
			if inv, ok := runtimeapi.InvocationFromContext(ctx); ok {
				seenInvocation = inv.ID()
			}
			if caps, _ := ctx.Value(workerCapabilitiesKey{}).(*workerCapabilities); caps != nil {
				seenScope = caps.scope
			}
			if err := emit([]byte(`{"seq":1}`), "1", "tick"); err != nil {
				return err
			}
			<-ctx.Done()
			seenCancel = true
			return ctx.Err()
		}, nil
	}); err != nil {
		t.Fatal(err)
	}

	baseCtx := context.WithValue(t.Context(), workerCapabilitiesKey{}, &workerCapabilities{scope: "scope-sse", token: "tok"})
	result, err := invokeGoWorker(baseCtx, workerabi.Invocation{
		Service: "events/service/feed", Operation: operation, Mode: "direct", Input: json.RawMessage(`{}`),
		Context: workerabi.InvocationContext{
			InvocationID: "inv-sse-1",
			Principal:    "user-42",
			Scope:        "scope-sse",
			Metadata:     json.RawMessage(`{"request_type":"api-call","method":"GET","path":"/events"}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Events == nil {
		t.Fatal("expected non-nil event producer")
	}

	// Simulate serveInvocation executing the producer AFTER invokeGoWorker returned
	// and its defer enterState(state)() unwound goroutine state.
	streamCtx, cancel := context.WithCancel(t.Context())
	var emitted []string
	err = result.Events(streamCtx, func(data []byte, id, event string) error {
		emitted = append(emitted, id+":"+event+":"+string(data))
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("producer err = %v, want context.Canceled", err)
	}
	if !seenCancel || len(emitted) != 1 || emitted[0] != `1:tick:{"seq":1}` {
		t.Fatalf("emitted = %v, seenCancel = %v", emitted, seenCancel)
	}
	if seenReq.Path != "/events" || seenReq.InvocationID != "inv-sse-1" {
		t.Fatalf("CurrentRequest inside producer = %+v", seenReq)
	}
	if seenPrincipal != "user-42" {
		t.Fatalf("CurrentAuth().UID inside producer = %q, want user-42", seenPrincipal)
	}
	if seenInvocation != "inv-sse-1" {
		t.Fatalf("InvocationFromContext inside producer = %q, want inv-sse-1", seenInvocation)
	}
	if seenScope != "scope-sse" {
		t.Fatalf("workerCapabilities scope inside producer = %q, want scope-sse", seenScope)
	}
}
