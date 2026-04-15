package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPulseConfigEndpoint(t *testing.T) {
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
