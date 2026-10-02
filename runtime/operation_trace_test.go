package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devreport"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

func TestDurableAttemptsTraceSQLAndKeepDispatchParent(t *testing.T) {
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 32)}
	defer setTestReporter(reporter)()
	parent := &requestState{traceEnabled: true, trace: &traceSpan{traceID: strings.Repeat("a", 32), spanID: strings.Repeat("b", 16)}}
	source := withRuntimeInvocation(withState(context.Background(), parent), parent)
	encoded, err := durableInvocationMetadataJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	var previous string
	for _, failure := range []error{errors.New("failed"), nil} {
		ctx, end := enterDurableInvocation(context.Background(), "worker", "task", "job", time.Second, durableInvocationMetadataFromJSON(encoded), 0)
		span := stateFromContext(ctx).trace
		token, _ := runtimeapi.InvocationFromContext(ctx)
		if span == nil || span.traceID != parent.trace.traceID || span.parentSpanID != parent.trace.spanID || span.spanID == previous || token.TraceID() != span.traceID {
			t.Fatalf("attempt trace: %+v", span)
		}
		previous = span.spanID
		TraceDBQueryEnd(TraceDBQueryStart(ctx, "SELECT 1", 0), "SELECT 1", 1, nil)
		end(failure)
	}
	assertTraceChildren(t, reporter, "DURABLE", 2)
}

func TestCLITraceClosesOnPanicAndPreservesRecovery(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 16)}
	defer setTestReporter(reporter)()
	if err := RegisterContractCLIBinding(ContractCLIBindingRegistration{Address: "app/binding/cli", Command: []string{"probe"}, Invoke: func(ctx context.Context, _ []byte) (ContractCLIOutcome, error) {
		TraceDBQueryEnd(TraceDBQueryStart(ctx, "SELECT 1", 0), "SELECT 1", 1, nil)
		panic("private panic payload")
	}}); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_, _ = InvokeContractCLIBinding(context.Background(), "app/binding/cli", nil)
	}()
	assertTraceChildren(t, reporter, "CLI", 1)
}

func TestEventDeliveryCreatesTraceAndCorrelatesSQL(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 16)}
	defer setTestReporter(reporter)()
	bus := &fakeContractEventBus{}
	if err := RegisterContractEventBus("app/event_bus/events", bus); err != nil {
		t.Fatal(err)
	}
	if err := RegisterContractEventConsumer(ContractEventConsumerRegistration{Address: "app/binding/event", BusAddress: "app/event_bus/events", Channel: "test", ContractAddress: "app/event/test", ContractVersion: 1, Guarantee: "at_most_once", Identity: "std.workload_identity.event_consumer", Attempts: 1, Backoff: "none", Invoke: func(ctx context.Context, _ []byte) error {
		token, _ := runtimeapi.InvocationFromContext(ctx)
		if token.TraceID() != stateFromContext(ctx).trace.traceID {
			t.Fatal("event invocation lost trace")
		}
		TraceDBQueryEnd(TraceDBQueryStart(ctx, "SELECT 1", 0), "SELECT 1", 1, nil)
		return context.Canceled
	}}); err != nil {
		t.Fatal(err)
	}
	running, err := StartContractEventRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = running.Stop(context.Background()) }()
	err = bus.subscriptions[0].Handle(context.Background(), ContractEventMessage{ID: "message", BusAddress: "app/event_bus/events", Channel: "test", ContractAddress: "app/event/test", ContractVersion: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertTraceChildren(t, reporter, "EVENT", 1)
}

func assertTraceChildren(t *testing.T, reporter *devReporter, kind string, count int) {
	t.Helper()
	roots := map[string]*devreport.TraceSummary{}
	var queries []*devreport.TraceSummary
	for len(reporter.queue) > 0 {
		report := <-reporter.queue
		if s := report.TraceSummary; s != nil {
			if s.Type == kind {
				roots[s.SpanID] = s
			}
			if s.Type == "DB" {
				queries = append(queries, s)
			}
		}
	}
	if len(roots) != count || len(queries) != count {
		t.Fatalf("%s roots %d, SQL %d", kind, len(roots), len(queries))
	}
	for _, query := range queries {
		if query.ParentSpanID == nil || roots[*query.ParentSpanID] == nil || roots[*query.ParentSpanID].TraceID != query.TraceID {
			t.Fatalf("orphan SQL %+v", query)
		}
	}
}

func TestHTTPTracePropagationAndStreamingCompletion(t *testing.T) {
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 16)}
	defer setTestReporter(reporter)()
	state := &requestState{request: shared.Request{Service: "app"}, traceEnabled: true, trace: &traceSpan{traceID: strings.Repeat("a", 32), spanID: strings.Repeat("b", 16)}}
	req, _ := http.NewRequestWithContext(withState(context.Background(), state), "GET", "https://example.test", nil)
	req.Header = nil
	transport := &tracedRoundTripper{reporter: reporter, base: roundTripFunc(func(got *http.Request) (*http.Response, error) {
		traceID, parentID, ok := parseTraceParent(got.Header.Get("traceparent"))
		if !ok || traceID != state.trace.traceID || parentID == state.trace.spanID {
			t.Fatal("outgoing trace context missing")
		}
		incoming := &requestState{traceEnabled: true, request: shared.Request{Headers: got.Header}}
		startRequestTrace(incoming)
		if incoming.trace.traceID != traceID || incoming.trace.parentSpanID != parentID {
			t.Fatal("incoming context lost")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("hello"))}, nil
	})}
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("traceparent") != "" {
		t.Fatal("mutated caller request")
	}
	for len(reporter.queue) > 0 {
		if (<-reporter.queue).TraceSummary != nil {
			t.Fatal("finished before reading body")
		}
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	var summaries int
	for len(reporter.queue) > 0 {
		if s := (<-reporter.queue).TraceSummary; s != nil {
			summaries++
			if s.Type != "HTTP" || s.IsError {
				t.Fatalf("HTTP summary %+v", s)
			}
		}
	}
	if summaries != 1 {
		t.Fatalf("finished %d times", summaries)
	}
}

func TestTraceParentRejectsMalformedIdentities(t *testing.T) {
	for _, input := range []string{"", "00-" + strings.Repeat("0", 32) + "-1111111111111111-01", "00-" + strings.Repeat("A", 32) + "-1111111111111111-01", "00-" + strings.Repeat("a", 32) + "-0000000000000000-01"} {
		if _, _, ok := parseTraceParent(input); ok {
			t.Fatalf("accepted %q", input)
		}
	}
}
