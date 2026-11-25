package main

import (
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

// The native host records a request span and a worker span beneath it. A
// summary query counts the request once and spends its limit on requests.
func TestWorkerTraceSummariesCountRequestsNotTheirWorkerSpans(t *testing.T) {
	now := time.Now().UTC()
	span := func(trace string, root bool, age, duration time.Duration) devdash.TraceSpanDetail {
		return devdash.TraceSpanDetail{TraceSummary: devdash.TraceSummary{
			TraceID: trace, IsRoot: root, StartedAt: now.Add(-age), DurationNanos: uint64(duration), ServiceName: "pricing",
		}}
	}
	spans := []devdash.TraceSpanDetail{
		span("older", true, 2*time.Second, 40*time.Millisecond),
		span("older", false, 2*time.Second, 30*time.Millisecond),
		span("newer", true, time.Second, 20*time.Millisecond),
		span("newer", false, time.Second, 10*time.Millisecond),
	}
	items := workerTraceSummaries(spans, devdash.TraceQuery{Limit: 2})
	if len(items) != 2 || items[0].TraceID != "newer" || items[1].TraceID != "older" || !items[0].IsRoot || !items[1].IsRoot {
		for _, item := range items {
			t.Logf("trace %s root=%v", item.TraceID, item.IsRoot)
		}
		t.Fatalf("summary query returned %d spans, want the two requests, newest first", len(items))
	}
	summary := buildInspectMetricsSummary(items)
	if summary.TraceCount != 2 || summary.MinDurationMS != 20 || summary.MaxDurationMS != 40 || summary.AvgDurationMS != 30 {
		t.Fatalf("metrics counted worker spans: %+v", summary)
	}
	// The host already selected the spans of the requested trace.
	if detail := workerTraceSummaries(spans[:2], devdash.TraceQuery{TraceID: "older"}); len(detail) != 2 {
		t.Fatalf("trace query returned %d spans, want the request and its worker span", len(detail))
	}
}
