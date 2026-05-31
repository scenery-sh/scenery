package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pbrazdil/onlava/internal/devdash"
)

func TestRenderDevConsoleShowsSourcesLogsAndExpandedJSON(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 5, 31, 12, 44, 1, 223000000, time.UTC)
	event := devdash.DevEvent{
		ID:        42,
		SessionID: "feature-x",
		Source:    devdash.DevSource{ID: "worker:typescript", Kind: "worker", Name: "typescript", PID: "12351", Status: "running"},
		Level:     "error",
		Message:   "activity failed",
		Fields:    []byte(`{"activity":"SyncUser","attempt":2}`),
		Raw:       `ERROR activity failed activity=SyncUser attempt=2`,
		Parse:     devdash.DevEventParse{Format: "level-text", OK: true},
		CreatedAt: at,
	}
	snapshot := devConsoleSnapshot{
		AppName:    "billing",
		SessionID:  "feature-x",
		Selected:   "worker:typescript",
		ErrorsOnly: true,
		Expanded:   true,
		Sources: buildDevConsoleSources([]devdash.DevSource{
			{ID: "api", Kind: "app", Name: "api", PID: "12345", Status: "running"},
			{ID: "worker:typescript", Kind: "worker", Name: "typescript", PID: "12351", Status: "running"},
		}, []devdash.DevEvent{event}),
		Events: []devdash.DevEvent{event},
	}

	out := renderDevConsole(snapshot)
	for _, want := range []string{
		"onlava dev session: billing / feature-x",
		"worker:typescript",
		"activity failed",
		"activity=SyncUser",
		"event json",
		`"schema_version": "onlava.dev.event.v1"`,
		"q quit",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered console missing %q:\n%s", want, out)
		}
	}
}

func TestAttachTUIFallsBackToLogsWhenNotTerminal(t *testing.T) {
	prev := runOnlavaLogsFunc
	defer func() { runOnlavaLogsFunc = prev }()
	called := false
	runOnlavaLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		called = true
		got := strings.Join(args, "\x00")
		want := strings.Join([]string{"--follow", "--session", "session-123", "--limit", "200", "--stream", "all", "--source", "api"}, "\x00")
		if got != want {
			t.Fatalf("fallback logs args = %#v, want %#v", args, strings.Split(want, "\x00"))
		}
		return nil
	}
	if err := attachCommand([]string{"--tui", "--session", "session-123", "--source", "api"}); err != nil {
		t.Fatalf("attachCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected logs fallback")
	}
}
