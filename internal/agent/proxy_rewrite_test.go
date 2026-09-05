package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyRewritePreservesRoutesAndTrustsOnlyEdgeForwarding(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		var outbound *http.Request
		server := &Server{edgeToken: "edge-token", tcpTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			outbound = req
			response := httptest.NewRecorder()
			response.WriteHeader(http.StatusNoContent)
			return response.Result(), nil
		})}
		req := httptest.NewRequest(http.MethodGet, "http://app.test:4001/api/items?key=a%2Fb", nil)
		req.RemoteAddr = "127.0.0.1:43000"
		req.Header.Set("X-Forwarded-For", "203.0.113.10")
		req.Header.Set("X-Forwarded-Host", "spoof.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "443")
		req.Header.Set("Forwarded", "host=spoof.test")
		req.Header.Set("Connection", "X-Scenery-Route-Prefix, X-Forwarded-Host")
		if trusted {
			req.Header.Set("X-Scenery-Edge-Token", "edge-token")
		}
		response := httptest.NewRecorder()
		server.proxyBackendWithOptions(response, req, Backend{Addr: "backend.test"}, proxyBackendOptions{stripPrefix: "/api", routePrefix: "/api"})
		if response.Code != http.StatusNoContent || outbound == nil {
			t.Fatalf("proxy did not call backend: %d", response.Code)
		}
		if outbound.URL.Host != "backend.test" || outbound.Host != "app.test:4001" || outbound.URL.Path != "/items" || outbound.URL.RawQuery != "key=a%2Fb" {
			t.Fatalf("outbound request = %s host=%s", outbound.URL, outbound.Host)
		}
		forwardedFor, proto, port := "127.0.0.1", "http", "4001"
		if trusted {
			forwardedFor, proto, port = "203.0.113.10, 127.0.0.1", "https", "443"
		}
		for name, want := range map[string]string{"X-Forwarded-For": forwardedFor, "X-Forwarded-Host": "app.test:4001", "X-Forwarded-Proto": proto, "X-Forwarded-Port": port, "X-Scenery-Route-Prefix": "/api", "Forwarded": ""} {
			if got := outbound.Header.Get(name); got != want {
				t.Errorf("trusted=%v %s=%q, want %q", trusted, name, got, want)
			}
		}
	}
}
