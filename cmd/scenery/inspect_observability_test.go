package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

func TestRunSceneryInspectTracesWithFilters(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
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
	if payload.SchemaVersion != inspectTracesSchema {
		t.Fatalf("schema = %q", payload.SchemaVersion)
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
	cacheRoot := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
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
	if payload.SchemaVersion != inspectMetricsSchema {
		t.Fatalf("schema = %q", payload.SchemaVersion)
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
	cacheRoot := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
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
