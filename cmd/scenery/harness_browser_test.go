package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessUICommandWithDashboardURLAndFakeRunner(t *testing.T) {
	root := filepath.Join(t.ArtifactDir(), "harness-ui-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"harnessapp","id":"harness-dev"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := runHarnessUIBrowserChecksFunc
	t.Cleanup(func() { runHarnessUIBrowserChecksFunc = prev })
	runHarnessUIBrowserChecksFunc = func(_ context.Context, routes []harnessUIRouteSpec, _ string, headed bool) (harnessUIBrowserResult, error) {
		if headed {
			t.Fatal("headed should be false")
		}
		if len(routes) == 0 || routes[0].Path != "http://127.0.0.1:9401/harness-dev" {
			t.Fatalf("routes = %#v", routes)
		}
		return harnessUIBrowserResult{
			Routes: []harnessUIRoute{{
				Name:       "dashboard-home",
				URL:        routes[0].Path,
				OK:         true,
				DurationMS: 1,
				Markers:    []harnessUIMarker{{Selector: `[data-scenery-ui="AppShell"]`, Count: 1, Found: true}},
				Journey: []harnessUIJourneyResult{{
					Name:     "session/app selector visible",
					Kind:     "selector",
					Selector: `[data-scenery-ui="AppSelector"]`,
					Count:    1,
					Found:    true,
					Required: true,
				}},
				DOMSnapshot: ".scenery/harness/ui/dom/dashboard-home.json",
			}},
			Artifacts: []harnessArtifact{
				{Name: "dom:dashboard-home", Path: ".scenery/harness/ui/dom/dashboard-home.json", SchemaVersion: "scenery.harness.ui.dom.v1", Exists: true},
				{Name: "console", Path: ".scenery/harness/ui/console.jsonl", Exists: true},
			},
		}, nil
	}

	var out bytes.Buffer
	err := runSceneryHarnessUI(context.Background(), &out, []string{
		"--app-root", root,
		"--dashboard-url", "http://127.0.0.1:9401/harness-dev",
		"--json",
		"--write",
	})
	if err != nil {
		t.Fatalf("runSceneryHarnessUI: %v\n%s", err, out.String())
	}
	var payload harnessUIResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.harness.ui.v1" || !payload.OK {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Wrote == "" {
		t.Fatal("expected wrote path")
	}
	if len(payload.Evidence) == 0 || payload.Evidence[0].ReproCommand == "" {
		t.Fatalf("expected top-level evidence: %+v", payload.Evidence)
	}
	if len(payload.Routes) == 0 || payload.Routes[0].Evidence == nil {
		t.Fatalf("expected route evidence: %+v", payload.Routes)
	}
	if payload.Routes[0].DOMSnapshot == "" || len(payload.Routes[0].Journey) == 0 {
		t.Fatalf("expected route DOM and journey: %+v", payload.Routes[0])
	}
	if _, err := os.Stat(payload.Wrote); err != nil {
		t.Fatalf("expected written result: %v", err)
	}
}

func TestBuildHarnessUIRoutesIncludesSemanticJourneys(t *testing.T) {
	routes := buildHarnessUIRoutes("http://127.0.0.1:9401/demo")
	byName := map[string]harnessUIRouteSpec{}
	for _, route := range routes {
		byName[route.Name] = route
	}
	for _, name := range []string{"dashboard-home", "api-explorer", "service-catalog", "traces", "db-explorer", "cron", "observability"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing route %q in %#v", name, routes)
		}
	}
	checks := []struct {
		route string
		want  string
	}{
		{"dashboard-home", "session/app selector visible"},
		{"api-explorer", "request form renders"},
		{"service-catalog", "service count visible"},
		{"traces", "trace table or empty state visible"},
		{"db-explorer", "database list or unavailable state visible"},
		{"cron", "cron status cards visible"},
		{"observability", "worker status card visible"},
	}
	for _, check := range checks {
		if !harnessUIRouteHasCheck(byName[check.route], check.want) {
			t.Fatalf("route %s missing check %q: %#v", check.route, check.want, byName[check.route].Checks)
		}
	}
	if len(byName["api-explorer"].Actions) == 0 {
		t.Fatalf("api explorer should include an endpoint-open journey action")
	}
	if len(byName["traces"].Actions) == 0 || !byName["traces"].Actions[0].Optional {
		t.Fatalf("traces should include an optional trace-detail journey action")
	}
}

func harnessUIRouteHasCheck(route harnessUIRouteSpec, name string) bool {
	for _, check := range route.Checks {
		if check.Name == name {
			return true
		}
	}
	return false
}

func TestParseHarnessUIArgsRejectsUnknownFlags(t *testing.T) {
	if _, err := parseHarnessUIArgs([]string{"--wat"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
}

func TestHarnessUIDevProcessScanDevOutputReportsCompileError(t *testing.T) {
	proc := &harnessUIDevProcess{output: &safeLineTail{limit: 10}}
	ready := make(chan harnessUIDevSignal, 1)
	proc.scanDevOutput(strings.NewReader(`{"type":"process.compile-error","data":{"error":"fatal error: 'torch/torch.h' file not found"}}`+"\n"), ready)

	select {
	case signal := <-ready:
		if signal.err == nil {
			t.Fatal("expected compile-error signal")
		}
		if !strings.Contains(signal.err.Error(), "torch/torch.h") {
			t.Fatalf("signal error = %v", signal.err)
		}
	default:
		t.Fatal("expected readiness waiter signal")
	}
}
