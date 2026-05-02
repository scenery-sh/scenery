package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onlava.com/internal/devdash"
)

func TestParseLogsArgs(t *testing.T) {
	opts, err := parseLogsArgs([]string{"--app-root", "/tmp/app", "--limit", "50", "--stream", "stderr", "--follow", "--jsonl"})
	if err != nil {
		t.Fatalf("parseLogsArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Limit != 50 || opts.Stream != "stderr" || !opts.Follow || !opts.JSONL {
		t.Fatalf("unexpected logs options: %#v", opts)
	}
}

func TestParseLogsArgsTreatsJSONAsAliasForJSONL(t *testing.T) {
	opts, err := parseLogsArgs([]string{"--json"})
	if err != nil {
		t.Fatalf("parseLogsArgs returned error: %v", err)
	}
	if !opts.JSONL {
		t.Fatalf("expected JSONL mode, got %#v", opts)
	}
}

func TestRunOnlavaLogsReadsStoredOutput(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.0\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	if err := store.WriteProcessOutput(ctx, devdash.ProcessOutput{
		AppID:  "logsapp",
		PID:    "123",
		Stream: "stdout",
		Output: []byte("first line\n"),
	}); err != nil {
		t.Fatalf("WriteProcessOutput stdout: %v", err)
	}
	if err := store.WriteProcessOutput(ctx, devdash.ProcessOutput{
		AppID:  "logsapp",
		PID:    "123",
		Stream: "stderr",
		Output: []byte("second line\n"),
	}); err != nil {
		t.Fatalf("WriteProcessOutput stderr: %v", err)
	}

	var buf bytes.Buffer
	if err := runOnlavaLogs(ctx, &buf, []string{"--app-root", root, "--limit", "10"}); err != nil {
		t.Fatalf("runOnlavaLogs returned error: %v", err)
	}
	if got := buf.String(); got != "first line\nsecond line\n" {
		t.Fatalf("logs output = %q", got)
	}
}

func TestRunOnlavaLogsFiltersStream(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.0\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	for _, item := range []devdash.ProcessOutput{
		{AppID: "logsapp", PID: "123", Stream: "stdout", Output: []byte("out\n")},
		{AppID: "logsapp", PID: "123", Stream: "stderr", Output: []byte("err\n")},
	} {
		if err := store.WriteProcessOutput(ctx, item); err != nil {
			t.Fatalf("WriteProcessOutput: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := runOnlavaLogs(ctx, &buf, []string{"--app-root", root, "--stream", "stderr"}); err != nil {
		t.Fatalf("runOnlavaLogs returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "err" {
		t.Fatalf("stderr logs output = %q", got)
	}
}

func TestRunOnlavaLogsJSONL(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.0\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	if err := store.WriteProcessOutput(ctx, devdash.ProcessOutput{
		AppID:  "logsapp",
		PID:    "123",
		Stream: "stdout",
		Output: []byte("json line\n"),
	}); err != nil {
		t.Fatalf("WriteProcessOutput stdout: %v", err)
	}

	var buf bytes.Buffer
	if err := runOnlavaLogs(ctx, &buf, []string{"--app-root", root, "--jsonl"}); err != nil {
		t.Fatalf("runOnlavaLogs returned error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("jsonl lines = %d\n%s", len(lines), buf.String())
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		App           struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"app"`
		PID       string `json:"pid"`
		Stream    string `json:"stream"`
		Output    string `json:"output"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &payload); err != nil {
		t.Fatalf("json.Unmarshal(jsonl): %v\n%s", err, lines[0])
	}
	if payload.SchemaVersion != "onlava.logs.event.v1" {
		t.Fatalf("schema_version = %q", payload.SchemaVersion)
	}
	if payload.App.Name != "logsapp" || payload.App.Root != root {
		t.Fatalf("app = %+v", payload.App)
	}
	if payload.PID != "123" || payload.Stream != "stdout" || payload.Output != "json line\n" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.CreatedAt == "" {
		t.Fatal("expected created_at")
	}
}
