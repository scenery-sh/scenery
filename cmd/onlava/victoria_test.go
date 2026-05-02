package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onlava.com/internal/devdash"
)

func TestVictoriaEnabledDefaultsToTrue(t *testing.T) {
	t.Setenv("ONLAVA_DEV_VICTORIA", "")
	if !victoriaEnabled() {
		t.Fatal("victoriaEnabled() = false, want true")
	}
}

func TestVictoriaEnabledCanBeDisabled(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ONLAVA_DEV_VICTORIA", value)
			if victoriaEnabled() {
				t.Fatalf("victoriaEnabled() with %q = true, want false", value)
			}
		})
	}
}

func TestVictoriaArchiveName(t *testing.T) {
	name, err := victoriaArchiveName(victoriaComponentSpec{
		ArchiveSlug: "victoria-traces",
		Version:     "v0.8.1",
	})
	if err != nil {
		t.Fatalf("victoriaArchiveName: %v", err)
	}
	if !strings.HasPrefix(name, "victoria-traces-") || !strings.HasSuffix(name, "-v0.8.1.tar.gz") {
		t.Fatalf("archive name = %q", name)
	}
}

func TestChecksumForArchive(t *testing.T) {
	body := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  victoria-traces-linux-amd64-v0.8.1.tar.gz\n"
	got := checksumForArchive(body, "victoria-traces-linux-amd64-v0.8.1.tar.gz")
	if got != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("checksum = %q", got)
	}
}

func TestResolveVictoriaBinaryPrefersExplicitEnv(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "victoria-traces-prod")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	spec := victoriaComponentSpec{
		BinaryName: "victoria-traces-prod",
		EnvPrefix:  "ONLAVA_VICTORIA_TRACES",
	}
	t.Setenv("ONLAVA_VICTORIA_TRACES_BIN", binary)

	got, err := resolveVictoriaBinary(context.Background(), spec, filepath.Join(dir, "bin"), false)
	if err != nil {
		t.Fatalf("resolveVictoriaBinary: %v", err)
	}
	if got != binary {
		t.Fatalf("binary = %q, want %q", got, binary)
	}
}

func TestVictoriaStackEnv(t *testing.T) {
	stack := &victoriaStack{components: []*victoriaComponent{
		{
			spec: victoriaComponentSpec{
				OTELVar:           "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
				OnlavaURLVar:      "ONLAVA_VICTORIA_TRACES_URL",
				OnlavaEndpointVar: "ONLAVA_VICTORIA_TRACES_ENDPOINT",
			},
			baseURL:     "http://127.0.0.1:10428",
			endpointURL: "http://127.0.0.1:10428/insert/opentelemetry/v1/traces",
		},
	}}

	env := stack.Env()
	if !containsString(env, "ONLAVA_DEV_OBSERVABILITY_BACKEND=victoria") {
		t.Fatalf("env missing backend marker: %v", env)
	}
	if !containsString(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:10428/insert/opentelemetry/v1/traces") {
		t.Fatalf("env missing OTLP endpoint: %v", env)
	}
}

func TestStartVictoriaComponentReusesOccupiedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	component, err := startVictoriaComponent(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "bin"), victoriaComponentSpec{
		Name:              "traces",
		DisplayName:       "VictoriaTraces",
		DefaultPort:       port,
		EndpointPath:      "/insert/opentelemetry/v1/traces",
		StorageDir:        "traces-data",
		OTELVar:           "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		OnlavaURLVar:      "ONLAVA_VICTORIA_TRACES_URL",
		OnlavaEndpointVar: "ONLAVA_VICTORIA_TRACES_ENDPOINT",
	}, false, nil)
	if err != nil {
		t.Fatalf("startVictoriaComponent: %v", err)
	}
	if !component.external {
		t.Fatal("component.external = false, want true")
	}
	if component.endpointURL == "" {
		t.Fatal("component endpoint URL is empty")
	}
}

func TestBuildOTLPTracePayload(t *testing.T) {
	endpoint := "Hello"
	payload := buildOTLPTracePayload(&devdash.TraceSummary{
		AppID:         "app",
		TraceID:       "00000000000000010000000000000002",
		SpanID:        "0000000000000003",
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     time.Unix(1, 2).UTC(),
		DurationNanos: uint64(10 * time.Millisecond),
		ServiceName:   "svc",
		EndpointName:  &endpoint,
	}, []*devdash.TraceEvent{
		{
			TraceID:   "00000000000000010000000000000002",
			SpanID:    "0000000000000003",
			EventID:   4,
			EventTime: time.Unix(1, 3).UTC(),
			Event: map[string]any{
				"span_event": map[string]any{"db": "query"},
			},
		},
	})

	resourceSpans := payload["resourceSpans"].([]any)
	scopeSpans := resourceSpans[0].(map[string]any)["scopeSpans"].([]any)
	spans := scopeSpans[0].(map[string]any)["spans"].([]any)
	span := spans[0].(map[string]any)
	if span["traceId"] != "00000000000000010000000000000002" {
		t.Fatalf("traceId = %v", span["traceId"])
	}
	if span["spanId"] != "0000000000000003" {
		t.Fatalf("spanId = %v", span["spanId"])
	}
	if span["name"] != "svc.Hello" {
		t.Fatalf("name = %v", span["name"])
	}
	if len(span["events"].([]any)) != 1 {
		t.Fatalf("events = %v", span["events"])
	}
}

func TestBuildOTLPLogPayloadIncludesTraceContext(t *testing.T) {
	payload := buildOTLPLogPayload(&devdash.LogEvent{
		AppID:     "app",
		TraceID:   "00000000000000010000000000000002",
		SpanID:    "0000000000000003",
		Level:     "info",
		Message:   "hello",
		Timestamp: time.Unix(1, 2).UTC(),
	})

	resourceLogs := payload["resourceLogs"].([]any)
	scopeLogs := resourceLogs[0].(map[string]any)["scopeLogs"].([]any)
	records := scopeLogs[0].(map[string]any)["logRecords"].([]any)
	record := records[0].(map[string]any)
	if record["traceId"] != "00000000000000010000000000000002" {
		t.Fatalf("traceId = %v", record["traceId"])
	}
	if record["severityText"] != "INFO" {
		t.Fatalf("severityText = %v", record["severityText"])
	}
}

func TestVictoriaQueryTraceSummariesFromJaegerAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/jaeger/api/traces" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Fatalf("limit = %q, want 10", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{
					"traceID": "00000000000000010000000000000002",
					"processes": map[string]any{
						"p1": map[string]any{"serviceName": "app"},
					},
					"spans": []any{
						map[string]any{
							"traceID":       "00000000000000010000000000000002",
							"spanID":        "0000000000000003",
							"operationName": "svc.Hello",
							"startTime":     time.Unix(10, 0).UnixMicro(),
							"duration":      int64(25_000),
							"processID":     "p1",
							"tags": []any{
								map[string]any{"key": "onlava.service", "type": "string", "value": "svc"},
								map[string]any{"key": "onlava.endpoint", "type": "string", "value": "Hello"},
								map[string]any{"key": "onlava.is_error", "type": "bool", "value": false},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	stack := &victoriaStack{components: []*victoriaComponent{{
		spec:    victoriaComponentSpec{Name: "traces"},
		baseURL: server.URL,
	}}}
	items, err := stack.QueryTraceSummaries(context.Background(), devdash.TraceQuery{
		AppID: "app",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryTraceSummaries: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ServiceName != "svc" || items[0].EndpointName == nil || *items[0].EndpointName != "Hello" {
		t.Fatalf("summary = %+v", items[0])
	}
	if items[0].DurationNanos != uint64(25*time.Millisecond) {
		t.Fatalf("duration = %d", items[0].DurationNanos)
	}
}

func TestVictoriaQueryTraceSummariesClampsJaegerLimit(t *testing.T) {
	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{
					"traceID":   "00000000000000010000000000000002",
					"processes": map[string]any{"p1": map[string]any{"serviceName": "app"}},
					"spans": []any{
						map[string]any{
							"traceID":       "00000000000000010000000000000002",
							"spanID":        "0000000000000003",
							"operationName": "svc.Hello",
							"startTime":     time.Unix(10, 0).UnixMicro(),
							"duration":      int64(25_000),
							"processID":     "p1",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	stack := &victoriaStack{components: []*victoriaComponent{{
		spec:    victoriaComponentSpec{Name: "traces"},
		baseURL: server.URL,
	}}}
	if _, err := stack.QueryTraceSummaries(context.Background(), devdash.TraceQuery{
		AppID: "app",
		Limit: 10000,
	}); err != nil {
		t.Fatalf("QueryTraceSummaries: %v", err)
	}
	if gotLimit != "1000" {
		t.Fatalf("limit = %q, want 1000", gotLimit)
	}
}

func TestVictoriaMarkClearedFiltersOlderTraces(t *testing.T) {
	stack := &victoriaStack{}
	clearedAt := time.Unix(20, 0).UTC()
	stack.MarkCleared("app", clearedAt)
	if got := stack.ClearedAt("app"); !got.Equal(clearedAt) {
		t.Fatalf("ClearedAt = %s, want %s", got, clearedAt)
	}
}
