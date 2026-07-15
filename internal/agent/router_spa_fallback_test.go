package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSPAFallbackSurfacesBackendRestart proves a backend dying between the
// original 404 and the SPA fallback request answers 503/Retry-After instead of
// leaking the stale 404 as "not found".
func TestSPAFallbackSurfacesBackendRestart(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" {
			// The fallback request lands after the backend restarted: drop the
			// connection without a response, like a supervised restart does.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		http.NotFound(w, req)
	}))
	defer backend.Close()

	s := &Server{}
	addr := strings.TrimPrefix(backend.URL, "http://")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/missing/route", nil)

	s.proxyBackendWithOptions(rec, req, Backend{Network: "tcp", Addr: addr}, proxyBackendOptions{spaFallback: true})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: a restarting backend must not masquerade as 404", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header on backend-restart response")
	}
}
