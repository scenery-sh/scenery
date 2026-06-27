package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

func TestRunSceneryConsoleFallsBackToLogsWhenRawModeFails(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")
	installLogsVictoriaStack(t)

	prevRaw := enterRawTerminal
	prevLogs := runSceneryLogsFunc
	defer func() {
		enterRawTerminal = prevRaw
		runSceneryLogsFunc = prevLogs
	}()

	enterRawTerminal = func(stdin *os.File) (func(), error) {
		return func() {}, errors.New("stty unavailable")
	}

	opts, err := parseLogsArgs([]string{"--app-root", root, "--source", "api"})
	if err != nil {
		t.Fatalf("parse logs args: %v", err)
	}
	called := false
	runSceneryLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		called = true
		want := []string{"--follow", "--limit", "200", "--stream", "all", "--app-root", root, "--source", "api"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("fallback logs args = %#v, want %#v", args, want)
		}
		return nil
	}

	var out bytes.Buffer
	if err := runSceneryConsole(context.Background(), os.Stdin, &out, opts); err != nil {
		t.Fatalf("runSceneryConsole returned error: %v", err)
	}
	if !called {
		t.Fatal("expected logs fallback")
	}
}

func TestDevConsoleRefreshUsesSelectedBackend(t *testing.T) {
	installLogsVictoriaStack(t,
		devdash.DevEvent{ID: 1, AppID: "logsapp", SessionID: "session-a", Source: devdash.DevSource{ID: "api", Kind: "app"}, Level: "info", Message: "ok", CreatedAt: time.Now().UTC()},
		devdash.DevEvent{ID: 2, AppID: "logsapp", SessionID: "session-a", Source: devdash.DevSource{ID: "worker:durable", Kind: "worker"}, Level: "error", Message: "boom", CreatedAt: time.Now().UTC()},
	)
	backend := resolveLogsVictoriaStackFunc(context.Background(), true)
	if backend == nil {
		t.Fatal("expected logs Victoria stack")
	}
	state := devConsoleState{
		opts:      logsOptions{Limit: 10},
		appID:     "logsapp",
		sessionID: "session-a",
		selected:  "worker:durable",
		errors:    true,
	}

	if err := state.refresh(context.Background(), backend); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(state.events) != 1 || state.events[0].Message != "boom" {
		t.Fatalf("state events = %+v", state.events)
	}
	if len(state.sources) != 2 {
		t.Fatalf("state sources = %+v", state.sources)
	}
}
