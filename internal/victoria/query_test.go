package victoria

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

func TestQueryTraceSummariesDefaultsToRecentWindow(t *testing.T) {
	var start, end string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/jaeger/api/traces" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		start = r.URL.Query().Get("start")
		end = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	stack := &Stack{components: []*Component{{
		spec:    ComponentSpec{Name: "traces"},
		baseURL: server.URL,
	}}}

	_, err := stack.QueryTraceSummaries(context.Background(), devdash.TraceQuery{AppID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if start == "" || end == "" {
		t.Fatalf("start/end missing: start=%q end=%q", start, end)
	}
	startTime, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	if time.Since(time.UnixMicro(startTime)) > defaultTraceSince+time.Minute {
		t.Fatalf("start too old: %s", time.UnixMicro(startTime))
	}
}

func TestTraceSummaryReadsTheServingGeneration(t *testing.T) {
	span := victoriaJaegerSpan{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", Tags: []victoriaJaegerTag{
		{Key: "scenery.generation", Type: "string", Value: "12"},
		{Key: "scenery.build_input_digest", Type: "string", Value: "sha256:build"},
	}}
	summary := traceSummaryFromVictoriaSpan("app", victoriaJaegerTrace{}, span)
	if summary == nil || summary.Generation != 12 || summary.BuildInputDigest != "sha256:build" {
		t.Fatalf("summary = %+v", summary)
	}
}
