package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
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
		"scenery console  billing",
		"worker:typescript",
		"activity failed",
		`"activity": "SyncUser"`,
		"event json",
		`"schema_version": "scenery.dev.event.v1"`,
		"q quit",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered console missing %q:\n%s", want, out)
		}
	}
}

func TestDevConsoleDiffRendererAvoidsFullClearDuringSteadyState(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	renderer := newConsoleDiffRenderer(&out, terminalSize{Width: 40, Height: 4})
	if err := renderer.Render("one\ntwo\nthree\nfour"); err != nil {
		t.Fatalf("initial render: %v", err)
	}
	out.Reset()
	if err := renderer.Render("one\nTWO\nthree\nfour"); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("steady-state render used full clear: %q", got)
	}
	if strings.Contains(got, "\x1b[1;1H") {
		t.Fatalf("unchanged first line was repainted: %q", got)
	}
	if !strings.Contains(got, "\x1b[2;1HTWO\x1b[K") {
		t.Fatalf("changed second line was not repainted precisely: %q", got)
	}

	out.Reset()
	renderer.Resize(terminalSize{Width: 80, Height: 4})
	if err := renderer.Render("one\nTWO\nthree\nfour"); err != nil {
		t.Fatalf("resize render: %v", err)
	}
	got = out.String()
	if strings.Count(got, "\x1b[2J") != 1 {
		t.Fatalf("resize render should use exactly one full clear: %q", got)
	}
	for _, want := range []string{
		"\x1b[1;1Hone\x1b[K",
		"\x1b[2;1HTWO\x1b[K",
		"\x1b[3;1Hthree\x1b[K",
		"\x1b[4;1Hfour\x1b[K",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resize render did not repaint row %q: %q", want, got)
		}
	}

	out.Reset()
	if err := renderer.Render("one\nTWO\nthree\nfour"); err != nil {
		t.Fatalf("post-resize steady-state render: %v", err)
	}
	if got := out.String(); strings.Contains(got, "\x1b[2J") {
		t.Fatalf("post-resize steady-state render used full clear again: %q", got)
	}
}

func TestRenderDevConsoleResponsiveLayoutsStayBounded(t *testing.T) {
	t.Parallel()

	events := make([]devdash.DevEvent, 0, 40)
	for i := 0; i < 40; i++ {
		events = append(events, devdash.DevEvent{
			ID:        int64(i + 1),
			SessionID: "feature-x",
			Source:    devdash.DevSource{ID: "api", Kind: "app", Name: "api", PID: "12345", Status: "running"},
			Level:     "info",
			Message:   strings.Repeat("long-message-", 12),
			CreatedAt: time.Date(2026, 5, 31, 12, 0, i, 0, time.UTC),
		})
	}
	snapshot := devConsoleSnapshot{
		AppName:   "billing",
		SessionID: "feature-x",
		Selected:  "all",
		Sources: buildDevConsoleSources([]devdash.DevSource{
			{ID: "api", Kind: "app", Name: "api", PID: "12345", Status: "running"},
			{ID: "frontend:web", Kind: "frontend", Name: "web", Status: "running"},
			{ID: "worker:typescript", Kind: "worker", Name: "typescript", PID: "12351", Status: "running"},
		}, events),
		Events: events,
	}
	for _, size := range []terminalSize{{Width: 80, Height: 24}, {Width: 200, Height: 60}} {
		snapshot.Width = size.Width
		snapshot.Height = size.Height
		out := renderDevConsole(snapshot)
		lines := strings.Split(out, "\n")
		if len(lines) != size.Height {
			t.Fatalf("%dx%d rendered %d lines", size.Width, size.Height, len(lines))
		}
		for i, line := range lines {
			if got := visibleStringWidth(line); got > size.Width {
				t.Fatalf("%dx%d line %d width = %d, want <= %d:\n%s", size.Width, size.Height, i+1, got, size.Width, out)
			}
		}
	}
}

func TestDevConsoleKeyNavigationSearchAndMouse(t *testing.T) {
	t.Parallel()

	state := devConsoleState{width: 80, height: 12, selected: "all"}
	for i := 0; i < 30; i++ {
		state.events = append(state.events, devdash.DevEvent{ID: int64(i + 1), Message: "line"})
	}
	state.handleKey(consoleKey{Kind: consoleKeyUp})
	if state.scroll != 1 {
		t.Fatalf("up scroll = %d, want 1", state.scroll)
	}
	state.handleKey(consoleKey{Kind: consoleKeyPageDown})
	if state.scroll != 0 {
		t.Fatalf("page down scroll = %d, want 0", state.scroll)
	}
	state.handleKey(consoleKey{Kind: consoleKeyMouseWheelUp})
	if state.scroll != 1 {
		t.Fatalf("mouse wheel scroll = %d, want 1", state.scroll)
	}
	state.handleRune('/')
	state.handleRune('b')
	state.handleRune('o')
	state.handleKey(consoleKey{Kind: consoleKeyBackspace})
	if !state.searching || state.search != "b" || state.scroll != 0 {
		t.Fatalf("search state = searching:%v search:%q scroll:%d", state.searching, state.search, state.scroll)
	}
	state.handleKey(consoleKey{Kind: consoleKeyEsc})
	if state.searching || state.search != "" {
		t.Fatalf("escape did not cancel search: searching:%v search:%q", state.searching, state.search)
	}
}

func TestReadConsoleKeyParsesEscapeSequences(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReader(strings.NewReader("\x1b[A\x1b[5~\x1b[6~\x1b[<64;20;10M\x1b[<65;20;10M"))
	wants := []consoleKeyKind{
		consoleKeyUp,
		consoleKeyPageUp,
		consoleKeyPageDown,
		consoleKeyMouseWheelUp,
		consoleKeyMouseWheelDown,
	}
	for _, want := range wants {
		key, err := readConsoleKey(reader)
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		if key.Kind != want {
			t.Fatalf("key kind = %v, want %v", key.Kind, want)
		}
	}
}

func TestAttachTUIFallsBackToLogsWhenNotTerminal(t *testing.T) {
	prev := runSceneryLogsFunc
	defer func() { runSceneryLogsFunc = prev }()
	called := false
	runSceneryLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		called = true
		got := strings.Join(args, "\x00")
		want := strings.Join([]string{"--follow", "--limit", "200", "--stream", "all", "--source", "api"}, "\x00")
		if got != want {
			t.Fatalf("fallback logs args = %#v, want %#v", args, strings.Split(want, "\x00"))
		}
		return nil
	}
	if err := attachCommand([]string{"--tui", "--source", "api"}); err != nil {
		t.Fatalf("attachCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected logs fallback")
	}
}

func TestDevConsoleRefreshUsesSelectedBackend(t *testing.T) {
	t.Parallel()

	backend := &fakeDevEventBackend{
		events: []devdash.DevEvent{
			{ID: 1, AppID: "logsapp", SessionID: "session-a", Source: devdash.DevSource{ID: "api", Kind: "app"}, Level: "info", Message: "ok", CreatedAt: time.Now().UTC()},
			{ID: 2, AppID: "logsapp", SessionID: "session-a", Source: devdash.DevSource{ID: "worker:typescript", Kind: "worker"}, Level: "error", Message: "boom", CreatedAt: time.Now().UTC()},
		},
		sources: []devdash.DevSource{
			{ID: "api", Kind: "app"},
			{ID: "worker:typescript", Kind: "worker"},
		},
	}
	state := devConsoleState{
		opts:      logsOptions{Limit: 10},
		appID:     "logsapp",
		sessionID: "session-a",
		selected:  "worker:typescript",
		errors:    true,
	}

	if err := state.refresh(context.Background(), backend); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if backend.lastQuery.SourceID != "worker:typescript" || backend.lastQuery.Level != "error" {
		t.Fatalf("backend query = %+v", backend.lastQuery)
	}
	if len(state.events) != 1 || state.events[0].Message != "boom" {
		t.Fatalf("state events = %+v", state.events)
	}
}

type fakeDevEventBackend struct {
	name      string
	events    []devdash.DevEvent
	sources   []devdash.DevSource
	lastQuery devdash.DevEventQuery
}

func (b *fakeDevEventBackend) ListDevEvents(ctx context.Context, query devdash.DevEventQuery) ([]devdash.DevEvent, error) {
	b.lastQuery = query
	out := slices.Clone(b.events)
	out = slices.DeleteFunc(out, func(event devdash.DevEvent) bool {
		if query.AppID != "" && event.AppID != query.AppID {
			return true
		}
		if query.SessionID != "" && event.SessionID != query.SessionID {
			return true
		}
		if query.SourceID != "" && event.Source.ID != query.SourceID {
			return true
		}
		if query.Level != "" && event.Level != query.Level {
			return true
		}
		return false
	})
	return out, nil
}

func (b *fakeDevEventBackend) ListDevSources(ctx context.Context, appID, sessionID string) ([]devdash.DevSource, error) {
	return slices.Clone(b.sources), nil
}

func (b *fakeDevEventBackend) BackendName() string {
	if b.name == "" {
		b.name = "fake"
	}
	return b.name
}
