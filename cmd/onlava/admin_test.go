package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pbrazdil/onlava/internal/devdash"
)

func TestParseAdminArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseAdminArgs([]string{"traces", "clear", "--json", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseAdminArgs returned error: %v", err)
	}
	if opts.Domain != "traces" || opts.Action != "clear" || !opts.JSON || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestRunOnlavaAdminRequiresJSON(t *testing.T) {
	t.Parallel()

	err := runOnlavaAdmin(context.Background(), []string{"traces", "clear"}, &bytes.Buffer{})
	if err == nil || err.Error() != "onlava admin currently requires --json" {
		t.Fatalf("runOnlavaAdmin() error = %v", err)
	}
}

func TestRunOnlavaAdminClearTraces(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"adminapp","id":"admin-id"}`)

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer store.Close()

	endpoint := "Ping"
	if err := store.AppendTraceSummary(context.Background(), &devdash.TraceSummary{
		AppID:         "admin-id",
		TraceID:       "trace-1",
		SpanID:        "span-1",
		Type:          "RPC",
		IsRoot:        true,
		StartedAt:     time.Now().UTC(),
		DurationNanos: 123,
		ServiceName:   "svc",
		EndpointName:  &endpoint,
	}); err != nil {
		t.Fatalf("AppendTraceSummary() error = %v", err)
	}

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaAdmin(context.Background(), []string{"traces", "clear", "--json"}, &out); err != nil {
		t.Fatalf("runOnlavaAdmin(traces clear) error = %v", err)
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Command       string `json:"command"`
		Data          struct {
			AppID   string `json:"app_id"`
			Cleared string `json:"cleared"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(admin traces): %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "onlava.admin.result.v1" || !payload.OK || payload.Command != "onlava admin traces clear" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Data.AppID != "admin-id" || payload.Data.Cleared != "traces" {
		t.Fatalf("data = %+v", payload.Data)
	}
	list, err := store.ListTraceSummaries(context.Background(), "admin-id", 10, "")
	if err != nil {
		t.Fatalf("ListTraceSummaries() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected traces cleared, got %d summaries", len(list))
	}
}
