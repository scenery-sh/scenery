package victoria

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"scenery.sh/internal/devdash"
)

type traceQueryTransport func(*http.Request) (*http.Response, error)

func (fn traceQueryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestTraceDetailScopesEverySpanAndPreservesSQLAndParents(t *testing.T) {
	trace := victoriaJaegerTrace{TraceID: strings.Repeat("a", 32), Processes: map[string]victoriaJaegerProcess{"app": {ServiceName: "app"}, "other": {ServiceName: "other"}}}
	makeSpan := func(id, app, session, parent string) victoriaJaegerSpan {
		span := victoriaJaegerSpan{TraceID: trace.TraceID, SpanID: id, ProcessID: app, StartTime: 100, Duration: 2, Tags: []victoriaJaegerTag{{Key: "scenery.application_id", Value: app}, {Key: "scenery.session_id", Value: session}, {Key: "scenery.trace.type", Value: "DB"}}, Logs: []victoriaJaegerLog{{Timestamp: 101, Fields: []victoriaJaegerTag{{Key: "event", Value: "scenery.event"}, {Key: "scenery.event", Value: `{"span_start":{"db":{"query":"SELECT $1"}}}`}}}}}
		if parent != "" {
			span.References = []victoriaJaegerReference{{TraceID: trace.TraceID, SpanID: parent, RefType: "CHILD_OF"}}
		}
		return span
	}
	trace.Spans = []victoriaJaegerSpan{makeSpan("root", "app", "current", ""), makeSpan("sql", "app", "current", "root"), makeSpan("foreign", "other", "current", "root"), makeSpan("old", "app", "old-session", "root")}
	data, _ := json.Marshal(victoriaJaegerResponse{Data: []victoriaJaegerTrace{trace}})
	previous := exportClient
	exportClient = &http.Client{Transport: traceQueryTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	defer func() { exportClient = previous }()
	stack := &Stack{components: []*Component{{spec: ComponentSpec{Name: "traces"}, baseURL: "http://backend.test"}}}
	detail, err := stack.GetTraceDetail(context.Background(), devdash.TraceQuery{AppID: "app", SessionID: "current", TraceID: trace.TraceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Spans) != 2 || detail.Spans[1].ParentSpanID == nil || *detail.Spans[1].ParentSpanID != "root" {
		t.Fatalf("scoped tree %+v", detail)
	}
	encoded, _ := json.Marshal(detail)
	if !strings.Contains(string(encoded), "SELECT $1") || strings.Contains(string(encoded), "foreign") || strings.Contains(string(encoded), "old-session") {
		t.Fatalf("detail %s", encoded)
	}
	detail, err = stack.GetTraceDetail(context.Background(), devdash.TraceQuery{AppID: "app", SessionID: "unknown", TraceID: trace.TraceID})
	if err != nil || len(detail.Spans) != 0 {
		t.Fatalf("wrong-session result %+v, %v", detail, err)
	}
}

func TestTraceDetailMissingAndOversizedBackendResults(t *testing.T) {
	previous := exportClient
	defer func() { exportClient = previous }()
	stack := &Stack{components: []*Component{{spec: ComponentSpec{Name: "traces"}, baseURL: "http://backend.test"}}}
	query := devdash.TraceQuery{AppID: "app", SessionID: "current", TraceID: strings.Repeat("a", 32)}
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		exportClient = &http.Client{Transport: traceQueryTransport(func(req *http.Request) (*http.Response, error) {
			body := "missing"
			if status == http.StatusOK {
				body = strings.Repeat(" ", (8<<20)+1)
			}
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		detail, err := stack.GetTraceDetail(context.Background(), query)
		if status == http.StatusNotFound {
			if err != nil || detail == nil || len(detail.Spans) != 0 {
				t.Fatalf("unknown trace: %+v %v", detail, err)
			}
		} else if !errors.Is(err, ErrTraceResultTooLarge) {
			t.Fatalf("unbounded backend response: %v", err)
		}
	}
}
