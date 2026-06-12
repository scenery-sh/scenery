package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSceneryConfigEndpoint(t *testing.T) {
	t.Setenv("SCENERY_DEV_ENDPOINTS", "1")
	SetAppConfig(AppConfig{Name: "demoapp-dev", ListenAddr: "127.0.0.1:4000"})
	SetPublicBaseURL("https://api.acme.localhost")

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/__scenery/config", nil)
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
	if body.AppID != "demoapp-dev" {
		t.Fatalf("appID = %q, want %q", body.AppID, "demoapp-dev")
	}
	if body.APIBaseURL != "https://api.acme.localhost" {
		t.Fatalf("apiBaseURL = %q, want %q", body.APIBaseURL, "https://api.acme.localhost")
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
		{method: http.MethodGet, path: "/__scenery/config"},
		{method: http.MethodGet, path: "/platform.Stats"},
		{method: http.MethodGet, path: "/debug/pprof/heap"},
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
	t.Setenv("SCENERY_DEV_ENDPOINTS", "1")
	SetAppConfig(AppConfig{Name: "demoapp-dev", ListenAddr: "127.0.0.1:4000"})
	SetPublicBaseURL("https://api.acme.localhost")

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
	if body.AppID != "demoapp-dev" {
		t.Fatalf("appID = %q, want %q", body.AppID, "demoapp-dev")
	}
	if body.APIBaseURL != "https://api.acme.localhost" {
		t.Fatalf("apiBaseURL = %q, want %q", body.APIBaseURL, "https://api.acme.localhost")
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
	if body.Profiles.CPU != "https://api.acme.localhost/debug/pprof/profile?seconds=30" {
		t.Fatalf("cpu profile URL = %q", body.Profiles.CPU)
	}
}

func TestPProfHeapEndpoint(t *testing.T) {
	t.Setenv("SCENERY_DEV_ENDPOINTS", "1")
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

	t.Setenv("SCENERY_CORS_ALLOW_ORIGINS", "https://example.com")
	headers = http.Header{}
	applyCORSHeaders(headers, req)
	if got := headers.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("allowlisted origin = %q", got)
	}
}

func TestShutdownDrainsStreamingRawEndpointsCleanly(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	handlerDone := make(chan struct{})
	RegisterEndpoint(&Endpoint{
		Service: "events",
		Name:    "Stream",
		Access:  Public,
		Raw:     true,
		Path:    "/events",
		Methods: []string{http.MethodGet},
		RawHandler: func(w http.ResponseWriter, req *http.Request) {
			defer close(handlerDone)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: first\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			// A well-behaved streaming handler blocks on the request context;
			// shutdown must cancel it so the response terminates cleanly.
			<-req.Context().Done()
			_, _ = io.WriteString(w, "data: bye\n\n")
		},
	})

	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	httpServer := httptest.NewServer(server.Handler)
	defer httpServer.Close()

	client := httpServer.Client()
	client.Timeout = 5 * time.Second
	client.Transport = &http.Transport{DisableCompression: true}

	req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(res.Body, first); err != nil {
		t.Fatalf("read first event: %v", err)
	}

	// Shutdown of the runtime server begins the drain even though the test
	// serves through httptest; RegisterOnShutdown fires on Shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streaming handler context was not canceled by shutdown drain")
	}

	rest, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("stream did not terminate cleanly: %v", err)
	}
	if !strings.Contains(string(rest), "data: bye\n\n") {
		t.Fatalf("missing final event before clean close, got %q", string(rest))
	}
}
