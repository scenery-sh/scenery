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
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".onlava.json"), []byte(`{"name":"harnessapp","id":"harness-dev"}`), 0o644); err != nil {
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
				Markers:    []harnessUIMarker{{Selector: `[data-onlava-ui="AppShell"]`, Count: 1, Found: true}},
			}},
			Artifacts: []harnessArtifact{{Name: "console", Path: ".onlava/harness/ui/console.jsonl", Exists: true}},
		}, nil
	}

	var out bytes.Buffer
	err := runOnlavaHarnessUI(context.Background(), &out, []string{
		"--app-root", root,
		"--dashboard-url", "http://127.0.0.1:9401/harness-dev",
		"--json",
		"--write",
	})
	if err != nil {
		t.Fatalf("runOnlavaHarnessUI: %v\n%s", err, out.String())
	}
	var payload harnessUIResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "onlava.harness.ui.v1" || !payload.OK {
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
	if _, err := os.Stat(payload.Wrote); err != nil {
		t.Fatalf("expected written result: %v", err)
	}
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
