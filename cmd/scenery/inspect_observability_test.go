package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

func TestFormerInspectObservabilitySubjectsAreInvalidRequests(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{"traces", "metrics"} {
		t.Run(subject, func(t *testing.T) {
			var out bytes.Buffer
			err := runSceneryInspect([]string{subject, "-o", "json"}, &out)
			if err == nil || cliExitCode(err) != 2 {
				t.Fatalf("runSceneryInspect(%q) error = %v, exit = %d", subject, err, cliExitCode(err))
			}
			if !strings.Contains(err.Error(), "use `scenery "+subject+" list`") {
				t.Fatalf("error = %q, want current command", err)
			}

			var machineOut bytes.Buffer
			rendered := renderMachineError(&machineOut, []string{"inspect", subject, "-o", "json"}, err)
			if cliExitCode(rendered) != 2 {
				t.Fatalf("rendered error = %v, exit = %d", rendered, cliExitCode(rendered))
			}
			var envelope struct {
				Diagnostics []struct {
					Code        string `json:"code"`
					ReportToken string `json:"report_token"`
				} `json:"diagnostics"`
			}
			if decodeErr := json.Unmarshal(machineOut.Bytes(), &envelope); decodeErr != nil {
				t.Fatalf("decode machine error: %v\n%s", decodeErr, machineOut.String())
			}
			if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8001" {
				t.Fatalf("diagnostics = %#v, want SCN8001", envelope.Diagnostics)
			}
			if envelope.Diagnostics[0].ReportToken != "" {
				t.Fatalf("invalid request unexpectedly emitted report token %q", envelope.Diagnostics[0].ReportToken)
			}
		})
	}
}

func TestRunSceneryInspectTracesWithFilters(t *testing.T) {
	root := t.TempDir()
	cacheRoot := isolateCommandCacheRoot(t)
	t.Setenv("SCENERY_DEV_VICTORIA", "0")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"obsapp","id":"obs-id"}`)

	openTestObservabilityStore(t, cacheRoot, root)

	var out bytes.Buffer
	if err := runObservabilityList(context.Background(), &out, "traces", []string{
		"--app-root", root,
		"-o", "json",
		"--session", "session-a",
		"--endpoint", "List",
		"--min-duration-ms", "2000",
		"--slowest",
	}); err != nil {
		t.Fatalf("traces list: %v\n%s", err, out.String())
	}

	var payload inspectTracesResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Kind != inspectTracesKind || payload.SchemaRevision != newCLIPayloadIdentity(inspectTracesKind).SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if payload.Query.SessionID != "session-a" {
		t.Fatalf("query session = %q", payload.Query.SessionID)
	}
	if len(payload.Warnings) == 0 {
		t.Fatal("expected VictoriaTraces warning")
	}
	if len(payload.Traces) != 0 {
		t.Fatalf("traces = %d, want 0: %+v", len(payload.Traces), payload.Traces)
	}
}

func TestRunSceneryInspectMetricsAggregatesTracesAndLogs(t *testing.T) {
	root := t.TempDir()
	cacheRoot := isolateCommandCacheRoot(t)
	t.Setenv("SCENERY_DEV_VICTORIA", "0")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"obsapp","id":"obs-id"}`)

	openTestObservabilityStore(t, cacheRoot, root)

	var out bytes.Buffer
	if err := runObservabilityList(context.Background(), &out, "metrics", []string{
		"--app-root", root,
		"-o", "json",
		"--session", "session-a",
		"--service", "tenants",
		"--since", "1h",
	}); err != nil {
		t.Fatalf("metrics list: %v\n%s", err, out.String())
	}

	var payload inspectMetricsResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Kind != inspectMetricsKind || payload.SchemaRevision != newCLIPayloadIdentity(inspectMetricsKind).SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if payload.Query.SessionID != "session-a" {
		t.Fatalf("query session = %q", payload.Query.SessionID)
	}
	if payload.Summary.TraceCount != 0 || payload.Summary.ErrorCount != 0 || payload.Summary.EventCount != 0 || payload.Summary.LogCount != 0 {
		t.Fatalf("summary = %+v", payload.Summary)
	}
	if len(payload.Warnings) < 2 {
		t.Fatalf("expected Victoria warnings, got %+v", payload.Warnings)
	}
	if len(payload.Services) != 0 {
		t.Fatalf("services = %+v", payload.Services)
	}
	if len(payload.Endpoints) != 0 {
		t.Fatalf("endpoints = %+v", payload.Endpoints)
	}
	if len(payload.Logs) != 0 {
		t.Fatalf("logs = %+v", payload.Logs)
	}
}

func TestRunSceneryInspectUsesSessionAppRecordWhenLatestAppRootDiffers(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	cacheRoot := isolateCommandCacheRoot(t)
	t.Setenv("SCENERY_DEV_VICTORIA", "0")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"obsapp","id":"obs-id"}`)

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	ctx := context.Background()
	for _, rec := range []devdash.AppRecord{
		{ID: "obs-id", SessionID: "session-a", Name: "obsapp", Root: root, Running: true, UpdatedAt: time.Now().UTC().Add(-time.Minute)},
		{ID: "obs-id", SessionID: "session-b", Name: "obsapp", Root: otherRoot, Running: true, UpdatedAt: time.Now().UTC()},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatalf("UpsertApp() error = %v", err)
		}
	}
	var out bytes.Buffer
	if err := runObservabilityList(context.Background(), &out, "traces", []string{
		"--app-root", root,
		"-o", "json",
		"--session", "session-a",
	}); err != nil {
		t.Fatalf("traces list: %v\n%s", err, out.String())
	}

	var payload inspectTracesResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if len(payload.Warnings) == 0 {
		t.Fatal("expected VictoriaTraces warning")
	}
	if len(payload.Traces) != 0 {
		t.Fatalf("traces = %+v", payload.Traces)
	}
}

func openTestObservabilityStore(t *testing.T, cacheRoot, appRoot string) *devdash.Store {
	t.Helper()
	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.UpsertApp(context.Background(), devdash.AppRecord{
		ID:   "obs-id",
		Name: "obsapp",
		Root: appRoot,
	}); err != nil {
		t.Fatalf("UpsertApp() error = %v", err)
	}
	return store
}
