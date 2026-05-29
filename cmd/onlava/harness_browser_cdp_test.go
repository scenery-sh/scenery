package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRunHarnessUIBrowserChecksWithLocalBrowser(t *testing.T) {
	if os.Getenv("ONLAVA_TEST_BROWSER") != "1" {
		t.Skip("set ONLAVA_TEST_BROWSER=1 to run the browser harness smoke")
	}
	if _, err := harnessBrowserExecutable(); err != nil {
		t.Skip(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><main data-onlava-ui="AppShell">ok</main></body></html>`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := runHarnessUIBrowserChecks(ctx, []harnessUIRouteSpec{{
		Name:    "home",
		Path:    server.URL,
		Markers: []string{`[data-onlava-ui="AppShell"]`},
	}}, t.TempDir(), false)
	if err != nil {
		t.Fatalf("runHarnessUIBrowserChecks() error = %v", err)
	}
	if len(result.Routes) != 1 || !result.Routes[0].OK {
		t.Fatalf("routes = %#v", result.Routes)
	}
	if len(result.Routes[0].Markers) != 1 || !result.Routes[0].Markers[0].Found {
		t.Fatalf("markers = %#v", result.Routes[0].Markers)
	}
}
