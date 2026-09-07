package main

import (
	"net/http"
	"testing"
)

func TestDashboardWebSocketOriginCheck(t *testing.T) {
	t.Parallel()
	req := &http.Request{Host: "localhost:4747", Header: http.Header{"Origin": []string{"http://localhost:4747"}}}
	if !dashboardCheckOrigin(req) {
		t.Fatal("expected same-origin dashboard websocket to pass")
	}
	req.Header.Set("Origin", "https://example.com")
	if dashboardCheckOrigin(req) {
		t.Fatal("expected cross-origin dashboard websocket to be rejected")
	}
}
