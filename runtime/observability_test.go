package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"onlava.com/runtime/shared"
)

func TestNewExternalStateAppliesSeparateLogAndTraceFilters(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	SetAppConfig(AppConfig{
		Name:       "testapp",
		ListenAddr: "127.0.0.1:4000",
		Observability: ObservabilityConfig{
			Logs: EndpointFilterConfig{
				ExcludeEndpoints: []string{"sync.*"},
			},
			Tracing: EndpointFilterConfig{
				IncludeEndpoints: []string{"sync.SyncGet"},
			},
		},
	})

	ep := &Endpoint{Service: "sync", Name: "SyncGet", Access: Public}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:4000/sync", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := newExternalState(ep, req, nil, nil, AuthInfo{})
	if state.logsEnabled {
		t.Fatal("logsEnabled = true, want false")
	}
	if !state.traceEnabled {
		t.Fatal("traceEnabled = false, want true")
	}
}

func TestStartRequestTraceStillCreatesSpanWhenOnlyLogsAreEnabled(t *testing.T) {
	state := &requestState{
		request:      sharedRequest("tenants", "Config", "/tenants/config"),
		logsEnabled:  true,
		traceEnabled: false,
	}
	startRequestTrace(state)
	if state.trace == nil {
		t.Fatal("state.trace = nil, want span for request logs")
	}
	if state.trace.traceID == "" {
		t.Fatal("traceID = empty")
	}
}

func TestOnlavaConsoleHandlerSkipsLogsForFilteredEndpoint(t *testing.T) {
	var out bytes.Buffer
	handler := newOnlavaConsoleHandler(&out)
	state := &requestState{
		request: sharedRequest("sync", "SyncGet", "/sync"),
		trace: &traceSpan{
			traceID: "trace-1",
			spanID:  "span-1",
			isRoot:  true,
		},
		logsEnabled:  false,
		traceEnabled: true,
	}
	restore := enterState(state)
	defer restore()

	record := slog.NewRecord(time.Date(2026, time.April, 14, 15, 13, 0, 0, time.Local), levelTrace, "request completed", 0)
	record.AddAttrs(slog.String("endpoint", "SyncGet"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("output = %q, want empty", got)
	}
}

func TestAuthHandlerLogsUseAuthEndpointFilter(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	SetAppConfig(AppConfig{
		Name:       "testapp",
		ListenAddr: "127.0.0.1:4000",
		Observability: ObservabilityConfig{
			Logs: EndpointFilterConfig{
				ExcludeEndpoints: []string{"auth.AuthHandler"},
			},
		},
	})

	var out bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(newOnlavaConsoleHandler(&out)))
	defer slog.SetDefault(prevLogger)

	state := &requestState{
		request: sharedRequest("jobs", "CreateApplicationDocument", "/jobs/applications/documents"),
		trace: &traceSpan{
			traceID: "trace-1",
			spanID:  "span-1",
			isRoot:  true,
		},
		logsEnabled:  true,
		traceEnabled: true,
	}
	restoreState := enterState(state)
	defer restoreState()

	handler := &AuthHandler{Service: "auth", Name: "AuthHandler"}
	logAuthHandlerStart(state, handler)
	logAuthHandlerCompleted(state, handler, AuthInfo{UID: "user-1"}, nil, time.Millisecond)

	if got := out.String(); got != "" {
		t.Fatalf("output = %q, want empty", got)
	}
}

func TestAuthHandlerLogsStillWriteWhenNotFiltered(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	SetAppConfig(AppConfig{
		Name:       "testapp",
		ListenAddr: "127.0.0.1:4000",
	})

	var out bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(newOnlavaConsoleHandler(&out)))
	defer slog.SetDefault(prevLogger)

	state := &requestState{
		request: sharedRequest("jobs", "CreateApplicationDocument", "/jobs/applications/documents"),
		trace: &traceSpan{
			traceID: "trace-1",
			spanID:  "span-1",
			isRoot:  true,
		},
		logsEnabled:  true,
		traceEnabled: true,
	}
	restoreState := enterState(state)
	defer restoreState()

	handler := &AuthHandler{Service: "auth", Name: "AuthHandler"}
	logAuthHandlerStart(state, handler)

	if got := out.String(); !strings.Contains(got, "running auth handler") {
		t.Fatalf("output = %q, want auth handler log", got)
	}
}

func TestEndpointFilterAllowsPathAndServiceEndpointPatterns(t *testing.T) {
	req := sharedRequest("tenants", "Config", "/tenants/config")

	if !endpointFilterAllows(EndpointFilterConfig{IncludeEndpoints: []string{"tenants.Config"}}, req) {
		t.Fatal("full service.endpoint pattern should match")
	}
	if !endpointFilterAllows(EndpointFilterConfig{IncludeEndpoints: []string{"/tenants/*"}}, req) {
		t.Fatal("path glob should match")
	}
	if endpointFilterAllows(EndpointFilterConfig{ExcludeEndpoints: []string{"sync.*"}}, req) != true {
		t.Fatal("unrelated exclude pattern should not block request")
	}
	if endpointFilterAllows(EndpointFilterConfig{ExcludeEndpoints: []string{"tenants.*"}}, req) {
		t.Fatal("exclude pattern should block request")
	}
}

func sharedRequest(service, endpoint, pathValue string) shared.Request {
	return shared.Request{
		Service:  service,
		Endpoint: endpoint,
		Path:     pathValue,
	}
}
