package main

import (
	"bytes"
	"context"
	"testing"
)

func TestParseTracesClearArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseTracesClearArgs([]string{"-o", "json", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseTracesClearArgs returned error: %v", err)
	}
	if opts.Domain != "traces" || opts.Action != "clear" || !opts.JSON || opts.AppRoot != "/tmp/app" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestRunTracesClear(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"adminapp","id":"admin-id"}`)

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runTracesClear(context.Background(), &out, []string{"-o", "json"}); err != nil {
		t.Fatalf("runTracesClear error = %v", err)
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
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON(traces clear): %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.traces.clear.v1" || !payload.OK || payload.Command != "scenery traces clear" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Data.AppID != "admin-id" || payload.Data.Cleared != "traces" {
		t.Fatalf("data = %+v", payload.Data)
	}
}
