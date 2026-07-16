package main

import (
	"bytes"
	"context"
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
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"harnessapp","id":"harness-dev","envs":{"local":{"default":true}}}`), 0o644); err != nil {
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
				Markers:    []harnessUIMarker{{Selector: `[data-scenery-ui="ConsoleHeaderNav"]`, Count: 1, Found: true}},
				Journey: []harnessUIJourneyResult{{
					Name:     "overview route rendered",
					Kind:     "selector",
					Selector: `[data-scenery-ui="ConsoleOverview"]`,
					Count:    1,
					Found:    true,
					Required: true,
				}},
				DOMSnapshot: ".scenery/harness/ui/dom/dashboard-home.json",
			}},
			Artifacts: []harnessArtifact{
				newHarnessArtifact("dom:dashboard-home", ".scenery/harness/ui/dom/dashboard-home.json", "scenery.harness.ui.dom", true),
				{Name: "console", Path: ".scenery/harness/ui/console.jsonl", Exists: true},
			},
		}, nil
	}

	var out bytes.Buffer
	err := runSceneryHarnessUI(context.Background(), &out, []string{
		"--app-root", root,
		"--dashboard-url", "http://127.0.0.1:9401/harness-dev",
		"-o", "json",
		"--write",
	})
	if err != nil {
		t.Fatalf("runSceneryHarnessUI: %v\n%s", err, out.String())
	}
	var payload harnessUIResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.harness.ui" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.harness.ui").SchemaRevision || !payload.OK {
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
	t.Parallel()

	routes := buildHarnessUIRoutes("http://127.0.0.1:9401", "demo")
	byName := map[string]harnessUIRouteSpec{}
	for _, route := range routes {
		byName[route.Name] = route
	}
	for _, name := range []string{"dashboard-home", "api-explorer", "service-catalog", "traces", "db-explorer", "cron", "symphony"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing route %q in %#v", name, routes)
		}
	}
	checks := []struct {
		route string
		want  string
	}{
		{"dashboard-home", "overview route rendered"},
	}
	for _, check := range checks {
		if !harnessUIRouteHasCheck(byName[check.route], check.want) {
			t.Fatalf("route %s missing check %q: %#v", check.route, check.want, byName[check.route].Checks)
		}
	}
	actionChecks := []struct {
		route string
		want  string
	}{
		{"api-explorer", "api page opens"},
		{"service-catalog", "catalog page opens"},
		{"traces", "traces page opens"},
		{"db-explorer", "databases page opens"},
		{"cron", "cron page opens"},
		{"symphony", "symphony board visible"},
	}
	for _, check := range actionChecks {
		if !harnessUIRouteHasAction(byName[check.route], check.want) {
			t.Fatalf("route %s missing action %q: %#v", check.route, check.want, byName[check.route].Actions)
		}
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

func harnessUIRouteHasAction(route harnessUIRouteSpec, name string) bool {
	for _, action := range route.Actions {
		if action.Name == name {
			return true
		}
	}
	return false
}

func TestParseHarnessUIArgsRejectsUnknownFlags(t *testing.T) {
	t.Parallel()

	if _, err := parseHarnessUIArgs([]string{"--wat"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
}

func TestHarnessUIDevProcessScanDevOutputReportsCompileError(t *testing.T) {
	t.Parallel()

	proc := &harnessUIDevProcess{output: &safeLineTail{limit: 10}}
	ready := make(chan harnessUIDevSignal, 1)
	var stream bytes.Buffer
	writer := newCLIEventWriter(&stream)
	if err := writer.event(runEvent{Type: "process.compile-error", Data: map[string]any{"error": "fatal error: 'torch/torch.h' file not found"}}); err != nil {
		t.Fatal(err)
	}
	proc.scanDevOutput(strings.NewReader(stream.String()), ready)

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
