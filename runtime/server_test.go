package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPulseConfigEndpoint(t *testing.T) {
	t.Setenv("PULSE_DEV_ENDPOINTS", "1")
	SetAppConfig(AppConfig{Name: "onlvnext-o5o2", ListenAddr: "127.0.0.1:4000"})
	SetPublicBaseURL("https://api.onlv.localhost")

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/__pulse/config", nil)
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want %q", got, "application/json")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want %q", got, "no-store")
	}

	var body struct {
		AppID      string `json:"appID"`
		APIBaseURL string `json:"apiBaseURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AppID != "onlvnext-o5o2" {
		t.Fatalf("appID = %q, want %q", body.AppID, "onlvnext-o5o2")
	}
	if body.APIBaseURL != "https://api.onlv.localhost" {
		t.Fatalf("apiBaseURL = %q, want %q", body.APIBaseURL, "https://api.onlv.localhost")
	}
}

func TestDevEndpointsAreDisabledByDefault(t *testing.T) {
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	for _, tt := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/__pulse/config"},
		{method: http.MethodGet, path: "/platform.Stats"},
		{method: http.MethodGet, path: "/debug/pprof/heap"},
		{method: http.MethodPost, path: "/__pulse/pubsub/clear"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, tt.path, nil)
		server.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestRawEndpointStreamsBeforeHandlerReturns(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	releaseStream := make(chan struct{})
	release := func() {
		select {
		case <-releaseStream:
		default:
			close(releaseStream)
		}
	}

	RegisterEndpoint(&Endpoint{
		Service: "events",
		Name:    "Stream",
		Access:  Public,
		Raw:     true,
		Path:    "/events",
		Methods: []string{http.MethodGet},
		RawHandler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: first\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-releaseStream
		},
	})

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	httpServer := httptest.NewServer(server.Handler)
	defer httpServer.Close()
	defer release()

	client := httpServer.Client()
	client.Timeout = 2 * time.Second
	client.Transport = &http.Transport{DisableCompression: true}

	req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("request should receive flushed raw response before handler returns: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := res.Header.Get("Content-Length"); got != "" {
		t.Fatalf("content-length = %q, want empty for streaming raw response", got)
	}

	got := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(res.Body, got); err != nil {
		t.Fatalf("read streamed body: %v", err)
	}
	if string(got) != "data: first\n\n" {
		t.Fatalf("body prefix = %q, want %q", string(got), "data: first\n\n")
	}
	release()
}

func TestPlatformStatsEndpoint(t *testing.T) {
	t.Setenv("PULSE_DEV_ENDPOINTS", "1")
	SetAppConfig(AppConfig{Name: "onlvnext-o5o2", ListenAddr: "127.0.0.1:4000"})
	SetPublicBaseURL("https://api.onlv.localhost")

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/platform.Stats", nil)
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want %q", got, "application/json")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want %q", got, "no-store")
	}

	var body PlatformStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AppID != "onlvnext-o5o2" {
		t.Fatalf("appID = %q, want %q", body.AppID, "onlvnext-o5o2")
	}
	if body.APIBaseURL != "https://api.onlv.localhost" {
		t.Fatalf("apiBaseURL = %q, want %q", body.APIBaseURL, "https://api.onlv.localhost")
	}
	if body.Process.PID == 0 {
		t.Fatal("expected process pid")
	}
	if body.Go.Goroutines <= 0 {
		t.Fatal("expected goroutine count")
	}
	if body.Memory.CurrentHeap.Bytes == 0 {
		t.Fatal("expected current heap bytes")
	}
	if body.Disk.Path == "" {
		t.Fatal("expected disk path")
	}
	if body.Profiles.CPU != "https://api.onlv.localhost/debug/pprof/profile?seconds=30" {
		t.Fatalf("cpu profile URL = %q", body.Profiles.CPU)
	}
}

func TestDevPubSubClearEndpointRequiresTokenAndCallsRuntime(t *testing.T) {
	prevGetenv := osGetenv
	prevClearer := localPubSubClearer
	defer func() {
		osGetenv = prevGetenv
		localPubSubClearer = prevClearer
	}()
	osGetenv = func(key string) string {
		if key == "PULSE_DEV_ENDPOINTS" {
			return "1"
		}
		if key == "PULSE_DEV_REPORT_TOKEN" {
			return "secret"
		}
		return ""
	}
	called := false
	localPubSubClearer = func(context.Context) (any, error) {
		called = true
		return []map[string]any{{"name": "events"}}, nil
	}
	SetAppConfig(AppConfig{Name: "onlvnext-o5o2", ListenAddr: "127.0.0.1:4000"})

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/__pulse/pubsub/clear", nil)
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unauthorized status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if called {
		t.Fatal("clearer called without token")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/__pulse/pubsub/clear", nil)
	req.Header.Set("Authorization", "Bearer secret")
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatal("clearer not called")
	}
}

func TestPProfHeapEndpoint(t *testing.T) {
	t.Setenv("PULSE_DEV_ENDPOINTS", "1")
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected heap profile body")
	}
}

func TestCORSRequiresDevModeOrAllowList(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	headers := http.Header{}
	applyCORSHeaders(headers, req)
	if got := headers.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("origin without allowlist = %q, want empty", got)
	}

	t.Setenv("PULSE_CORS_ALLOW_ORIGINS", "https://example.com")
	headers = http.Header{}
	applyCORSHeaders(headers, req)
	if got := headers.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("allowlisted origin = %q", got)
	}
}
