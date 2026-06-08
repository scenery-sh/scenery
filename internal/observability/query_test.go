package observability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestQueryLogsAppliesVictoriaLogsScope(t *testing.T) {
	t.Parallel()

	scope := testScope()
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		form = r.Form
		_, _ = io.WriteString(w, `{"_time":"2026-06-08T10:00:00Z","level":"error","source_id":"api","message":"failed","fields_json":"{\"route\":\"/sync\"}","trace_id":"trace-1","span_id":"span-1"}`+"\n")
	}))
	defer server.Close()

	result, err := QueryLogs(context.Background(), LogsQuery{
		BaseURL: server.URL,
		Scope:   scope,
		Query:   "error",
		Bounds:  testBounds(),
		Limit:   25,
		Timeout: time.Second,
		Fields:  []string{"message"},
	})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if form.Get("query") != "error" || form.Get("limit") != "25" || form.Get("timeout") != "1s" {
		t.Fatalf("unexpected form: %+v", form)
	}
	filters := strings.Join(form["extra_filters"], "\n")
	for _, want := range []string{`onlava.application_id:"demo"`, `onlava_session_id:"session-a"`} {
		if !strings.Contains(filters, want) {
			t.Fatalf("extra_filters %q missing %q", filters, want)
		}
	}
	if strings.Contains(filters, "app_root_hash") {
		t.Fatalf("logs scope should not require app root hash: %q", filters)
	}
	if result.SchemaVersion != LogsQuerySchema || len(result.Logs) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	entry := result.Logs[0]
	if entry.Time != "2026-06-08T10:00:00Z" || entry.Level != "error" || entry.Source != "api" || entry.Message != "failed" || entry.TraceID != "trace-1" || entry.Fields["route"] != "/sync" {
		t.Fatalf("unexpected log entry: %+v", entry)
	}
	if len(entry.Raw) != 1 || entry.Raw["message"] != "failed" {
		t.Fatalf("field selection not applied: %+v", entry.Raw)
	}
}

func TestTailLogsUsesStartOffsetAndSelfDescribingEntries(t *testing.T) {
	t.Parallel()

	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/tail" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		form = r.Form
		_, _ = io.WriteString(w, `{"_time":"2026-06-08T10:00:00Z","message":"tailed"}`+"\n")
	}))
	defer server.Close()

	var entries []LogsTailEntry
	err := TailLogs(context.Background(), LogsQuery{
		BaseURL: server.URL,
		Scope:   testScope(),
		Query:   "*",
		Bounds:  testBounds(),
		Timeout: time.Second,
	}, func(entry LogsTailEntry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		t.Fatalf("TailLogs: %v", err)
	}
	if form.Get("start_offset") != "15m" || form.Get("start") != "" || form.Get("end") != "" {
		t.Fatalf("unexpected tail form: %+v", form)
	}
	if len(entries) != 1 || entries[0].SchemaVersion != LogsTailEntrySchema || entries[0].Log.Message != "tailed" || entries[0].Scope.SessionID != "session-a" {
		t.Fatalf("unexpected tail entries: %+v", entries)
	}
}

func TestQueryMetricsAppliesExtraLabels(t *testing.T) {
	t.Parallel()

	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prometheus/api/v1/query_range" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		form = r.Form
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"onlava_request_duration_seconds","onlava_session_id":"session-a"},"values":[[1812450000,"0.2"]]}]}}`)
	}))
	defer server.Close()

	result, err := QueryMetrics(context.Background(), MetricsQuery{
		BaseURL: server.URL,
		Scope:   testScope(),
		PromQL:  "onlava_request_duration_seconds",
		Bounds:  testBounds(),
		Step:    5 * time.Second,
		Timeout: time.Second,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("QueryMetrics: %v", err)
	}
	if form.Get("query") != "onlava_request_duration_seconds" || form.Get("step") != "5s" {
		t.Fatalf("unexpected form: %+v", form)
	}
	labels := strings.Join(form["extra_label"], "\n")
	for _, want := range []string{"onlava_app=demo", "onlava_session_id=session-a", "onlava_app_root_hash=root123"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("extra_label %q missing %q", labels, want)
		}
	}
	if result.ResultType != "matrix" || len(result.Series) != 1 || len(result.Series[0].Values) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMetricsLabelsAndSeriesDecodeCatalogs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prometheus/api/v1/labels":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.Form.Get("match[]") != `onlava_request_duration_seconds` {
				t.Fatalf("labels match[] = %q", r.Form.Get("match[]"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []string{"z", "a"}})
		case "/prometheus/api/v1/series":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.Form.Get("match[]") != `onlava_request_duration_seconds` {
				t.Fatalf("match[] = %q", r.Form.Get("match[]"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []map[string]string{{"__name__": "onlava_request_duration_seconds"}}})
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer server.Close()

	labels, err := MetricsLabels(context.Background(), MetricsCatalogQuery{BaseURL: server.URL, Scope: testScope(), Bounds: testBounds(), Match: "onlava_request_duration_seconds", Limit: 10, Timeout: time.Second})
	if err != nil {
		t.Fatalf("MetricsLabels: %v", err)
	}
	if strings.Join(labels.Labels, ",") != "a,z" {
		t.Fatalf("labels = %+v", labels.Labels)
	}
	series, err := MetricsSeries(context.Background(), MetricsCatalogQuery{BaseURL: server.URL, Scope: testScope(), Bounds: testBounds(), Match: "onlava_request_duration_seconds", Limit: 10})
	if err != nil {
		t.Fatalf("MetricsSeries: %v", err)
	}
	if len(series.Series) != 1 || series.Series[0]["__name__"] != "onlava_request_duration_seconds" {
		t.Fatalf("series = %+v", series.Series)
	}
}

func testScope() QueryScope {
	return QueryScope{
		AppID:       "demo",
		SessionID:   "session-a",
		AppRoot:     "/tmp/demo",
		AppRootHash: "root123",
		Worktree:    "demo",
		Branch:      "feature/a",
		Enforced:    true,
	}
}

func testBounds() TimeBounds {
	return TimeBounds{
		Since: "15m",
		Start: time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
	}
}
