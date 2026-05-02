package runtime

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onlava.com/internal/wire"
)

func TestServerGzipCompressesAcceptedResponses(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, wire.CapabilitiesPath, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content-encoding = %q, want gzip", got)
	}
	if !varyContains(rec.Header(), "Accept-Encoding") {
		t.Fatalf("vary = %q, want Accept-Encoding", rec.Header().Values("Vary"))
	}

	body := gunzipResponseBody(t, rec.Body)
	var caps wire.Capabilities
	if err := json.Unmarshal(body, &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if caps.SchemaVersion != "onlava.wire.capabilities.v1" {
		t.Fatalf("schema_version = %q", caps.SchemaVersion)
	}
}

func TestServerLeavesResponsesUncompressedWithoutAcceptEncoding(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	httpServer, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, wire.CapabilitiesPath, nil)
	httpServer.Handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content-encoding = %q, want empty", got)
	}
	var caps wire.Capabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
}

func TestGzipSkipsNoBodyResponses(t *testing.T) {
	handler := withGzip(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content-encoding = %q, want empty", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", rec.Body.Len())
	}
}

func TestGzipSkipsEventStreamResponses(t *testing.T) {
	handler := withGzip(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ok\n\n")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content-encoding = %q, want empty", got)
	}
	if got := rec.Body.String(); got != "data: ok\n\n" {
		t.Fatalf("body = %q, want event stream body", got)
	}
}

func TestGzipSkipsUpgradeRequests(t *testing.T) {
	handler := withGzip(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("upgrade"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Upgrade", "websocket")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content-encoding = %q, want empty", got)
	}
	if got := rec.Body.String(); got != "upgrade" {
		t.Fatalf("body = %q, want upgrade", got)
	}
}

func TestRequestAcceptsGzipQualityValues(t *testing.T) {
	for _, tt := range []struct {
		header string
		want   bool
	}{
		{header: "gzip", want: true},
		{header: "br, gzip;q=0.5", want: true},
		{header: "gzip;q=0", want: false},
		{header: "br, *;q=1", want: true},
		{header: "gzip;q=0, *;q=1", want: false},
		{header: "", want: false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", tt.header)
		if got := requestAcceptsGzip(req); got != tt.want {
			t.Fatalf("requestAcceptsGzip(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func gunzipResponseBody(t *testing.T, body io.Reader) []byte {
	t.Helper()
	reader, err := gzip.NewReader(body)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	return data
}

func varyContains(headers http.Header, want string) bool {
	for _, value := range headers.Values("Vary") {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), want) {
				return true
			}
		}
	}
	return false
}
