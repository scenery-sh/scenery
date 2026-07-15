package victoria

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func TestVictoriaEnabledDefaultsToTrue(t *testing.T) {
	t.Setenv("SCENERY_DEV_VICTORIA", "")
	if !Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
}

func TestVictoriaEnabledCanBeDisabled(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SCENERY_DEV_VICTORIA", value)
			if Enabled() {
				t.Fatalf("Enabled() with %q = true, want false", value)
			}
		})
	}
}

func TestVictoriaArchiveName(t *testing.T) {
	t.Parallel()

	name, err := archiveName(ComponentSpec{
		ArchiveSlug: "victoria-traces",
		Version:     "v0.8.1",
	})
	if err != nil {
		t.Fatalf("archiveName: %v", err)
	}
	if !strings.HasPrefix(name, "victoria-traces-") || !strings.HasSuffix(name, "-v0.8.1.tar.gz") {
		t.Fatalf("archive name = %q", name)
	}
}

func TestChecksumForArchive(t *testing.T) {
	t.Parallel()

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

	spec := ComponentSpec{
		BinaryName: "victoria-traces-prod",
		EnvPrefix:  "SCENERY_VICTORIA_TRACES",
	}
	t.Setenv("SCENERY_VICTORIA_TRACES_BIN", binary)

	got, err := resolveBinary(context.Background(), spec, filepath.Join(dir, "bin"), false)
	if err != nil {
		t.Fatalf("resolveBinary: %v", err)
	}
	if got != binary {
		t.Fatalf("binary = %q, want %q", got, binary)
	}
}

func TestResolveVictoriaBinaryIgnoresPathBinary(t *testing.T) {
	dir := t.TempDir()
	pathBinary := filepath.Join(dir, "victoria-traces-prod")
	if err := os.WriteFile(pathBinary, []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	spec := ComponentSpec{
		DisplayName: "VictoriaTraces",
		ArchiveSlug: "victoria-traces",
		BinaryName:  "victoria-traces-prod",
		EnvPrefix:   "SCENERY_VICTORIA_TRACES",
	}
	_, err := resolveBinary(context.Background(), spec, filepath.Join(t.TempDir(), "bin"), false)
	if err == nil || !strings.Contains(err.Error(), "system PATH binaries are not used") {
		t.Fatalf("resolveBinary err = %v", err)
	}
}

func TestVictoriaStackEnv(t *testing.T) {
	t.Parallel()

	stack := &Stack{components: []*Component{
		{
			spec: ComponentSpec{
				OTELVar:            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
				SceneryURLVar:      "SCENERY_VICTORIA_TRACES_URL",
				SceneryEndpointVar: "SCENERY_VICTORIA_TRACES_ENDPOINT",
			},
			baseURL:     "http://127.0.0.1:10428",
			endpointURL: "http://127.0.0.1:10428/insert/opentelemetry/v1/traces",
		},
	}}

	env := stack.Env()
	if !containsString(env, "SCENERY_DEV_OBSERVABILITY_BACKEND=victoria") {
		t.Fatalf("env missing backend marker: %v", env)
	}
	if !containsString(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:10428/insert/opentelemetry/v1/traces") {
		t.Fatalf("env missing OTLP endpoint: %v", env)
	}
}

func TestVictoriaStackSubstrateRoundTrip(t *testing.T) {
	t.Parallel()

	stack := &Stack{}
	for _, spec := range ComponentSpecs() {
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", spec.DefaultPort)
		stack.components = append(stack.components, &Component{
			spec:        spec,
			baseURL:     baseURL,
			endpointURL: baseURL + spec.EndpointPath,
		})
	}
	req := stack.SubstrateRequest(123)
	if req.Kind != localagent.SubstrateVictoria || req.OwnerPID != 123 {
		t.Fatalf("substrate request = %+v", req)
	}
	substrate := localagent.Substrate{
		Kind:      req.Kind,
		URLs:      req.URLs,
		Endpoints: req.Endpoints,
	}
	roundTrip := FromSubstrate(substrate)
	if roundTrip == nil {
		t.Fatal("expected stack from substrate")
		return
	}
	env := roundTrip.Env()
	if !containsString(env, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:8428/opentelemetry/v1/metrics") {
		t.Fatalf("env = %+v", env)
	}
	urls := roundTrip.URLs()
	if urls["metrics"] != "http://127.0.0.1:8428/vmui" {
		t.Fatalf("urls = %+v", urls)
	}
	roundTrip.MarkExternal()
	if !roundTrip.components[0].external {
		t.Fatal("component not marked external")
	}
}

func TestVictoriaStackFromSubstrateRequiresAllComponents(t *testing.T) {
	t.Parallel()

	substrate := localagent.Substrate{
		Kind: localagent.SubstrateVictoria,
		URLs: map[string]string{
			"metrics": "http://127.0.0.1:8428",
		},
		Endpoints: map[string]string{
			"metrics": "http://127.0.0.1:8428/opentelemetry/v1/metrics",
		},
	}
	if stack := FromSubstrate(substrate); stack != nil {
		t.Fatalf("expected incomplete Victoria substrate to be rejected: %+v", stack)
	}
}

func TestURLAcceptsTCP(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	if !urlAcceptsTCP(server.URL) {
		t.Fatalf("urlAcceptsTCP(%q) = false, want true", server.URL)
	}
	if urlAcceptsTCP("http://127.0.0.1:1") {
		t.Fatal("urlAcceptsTCP on closed port = true, want false")
	}
}

func TestStartVictoriaComponentReusesOccupiedPort(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	component, err := startComponent(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "bin"), ComponentSpec{
		Name:               "traces",
		DisplayName:        "VictoriaTraces",
		DefaultPort:        port,
		EndpointPath:       "/insert/opentelemetry/v1/traces",
		StorageDir:         "traces-data",
		OTELVar:            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		SceneryURLVar:      "SCENERY_VICTORIA_TRACES_URL",
		SceneryEndpointVar: "SCENERY_VICTORIA_TRACES_ENDPOINT",
	}, false, nil)
	if err != nil {
		t.Fatalf("startComponent: %v", err)
	}
	if !component.external {
		t.Fatal("component.external = false, want true")
	}
	if component.endpointURL == "" {
		t.Fatal("component endpoint URL is empty")
	}
}

func TestStartVictoriaComponentsAttributesStartErrors(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "victoria-logs-prod")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCENERY_VICTORIA_LOGS_BIN", bin)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	results := startComponents(context.Background(), root, filepath.Join(root, "bin"), []ComponentSpec{
		{
			Name:         "metrics",
			DisplayName:  "VictoriaMetrics",
			DefaultPort:  ln.Addr().(*net.TCPAddr).Port,
			EndpointPath: "/opentelemetry/v1/metrics",
			StorageDir:   "metrics-data",
		},
		{
			Name:         "logs",
			DisplayName:  "VictoriaLogs",
			DefaultPort:  freeTestTCPPort(t),
			EndpointPath: "/insert/opentelemetry/v1/logs",
			StorageDir:   "logs-data",
			EnvPrefix:    "SCENERY_VICTORIA_LOGS",
		},
	}, false, nil)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].err != nil || results[0].component == nil || !results[0].component.external {
		t.Fatalf("occupied component result = %+v", results[0])
	}
	if results[1].err == nil || !strings.Contains(results[1].err.Error(), "VictoriaLogs exited before accepting TCP connections") {
		t.Fatalf("start error = %v, want VictoriaLogs attribution", results[1].err)
	}
}

func freeTestTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestVictoriaQueryTraceSummariesFromJaegerAPI(t *testing.T) {
	t.Parallel()

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
								map[string]any{"key": "scenery.service", "type": "string", "value": "svc"},
								map[string]any{"key": "scenery.endpoint", "type": "string", "value": "Hello"},
								map[string]any{"key": "scenery.session_id", "type": "string", "value": "session-a"},
								map[string]any{"key": "scenery.is_error", "type": "bool", "value": false},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	stack := &Stack{components: []*Component{{
		spec:    ComponentSpec{Name: "traces"},
		baseURL: server.URL,
	}}}
	items, err := stack.QueryTraceSummaries(context.Background(), devdash.TraceQuery{
		AppID:     "app",
		SessionID: "session-a",
		Limit:     10,
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
	if items[0].SessionID != "session-a" {
		t.Fatalf("session = %q", items[0].SessionID)
	}
	if items[0].DurationNanos != uint64(25*time.Millisecond) {
		t.Fatalf("duration = %d", items[0].DurationNanos)
	}
}

func TestVictoriaQueryTraceSummariesClampsJaegerLimit(t *testing.T) {
	t.Parallel()

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

	stack := &Stack{components: []*Component{{
		spec:    ComponentSpec{Name: "traces"},
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
	t.Parallel()

	stack := &Stack{}
	clearedAt := time.Unix(20, 0).UTC()
	stack.MarkCleared("app", clearedAt)
	if got := stack.ClearedAt("app"); !got.Equal(clearedAt) {
		t.Fatalf("ClearedAt = %s, want %s", got, clearedAt)
	}
}
