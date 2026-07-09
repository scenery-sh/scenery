package main

import (
	"bufio"
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
	"scenery.sh/internal/termstyle"
)

func TestRenderDevConsoleShowsSourcesLogsAndExpandedJSON(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 5, 31, 12, 44, 1, 223000000, time.UTC)
	event := devdash.DevEvent{
		ID:        42,
		SessionID: "feature-x",
		Source:    devdash.DevSource{ID: "worker:durable", Kind: "worker", Name: "durable", PID: "12351", Status: "running"},
		Level:     "error",
		Message:   "task failed",
		Fields:    []byte(`{"attempt":2,"task":"SyncUser"}`),
		Raw:       `ERROR task failed task=SyncUser attempt=2`,
		Parse:     devdash.DevEventParse{Format: "level-text", OK: true},
		CreatedAt: at,
	}
	snapshot := devConsoleSnapshot{
		AppName:    "billing",
		SessionID:  "feature-x",
		Selected:   "worker:durable",
		ErrorsOnly: true,
		Expanded:   true,
		Sources: buildDevConsoleSources([]devdash.DevSource{
			{ID: "api", Kind: "app", Name: "api", PID: "12345", Status: "running"},
			{ID: "worker:durable", Kind: "worker", Name: "durable", PID: "12351", Status: "running"},
		}, []devdash.DevEvent{event}),
		Events: []devdash.DevEvent{event},
	}

	out := renderDevConsole(snapshot)
	for _, want := range []string{
		"scenery console  billing",
		"worker:durable",
		"task failed",
		`"task": "SyncUser"`,
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

func TestRenderDevConsoleColorPaddingUsesVisibleWidths(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	palette := termstyle.New(&bytes.Buffer{})
	if !palette.Enabled() {
		t.Fatal("palette should be enabled by CLICOLOR_FORCE")
	}

	at := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	info := renderDevConsoleEventLine(devdash.DevEvent{
		Source:    devdash.DevSource{ID: "api"},
		Level:     "info",
		Message:   "info-message",
		CreatedAt: at,
	}, "", palette, 120)
	errLine := renderDevConsoleEventLine(devdash.DevEvent{
		Source:    devdash.DevSource{ID: "api"},
		Level:     "error",
		Message:   "error-message",
		CreatedAt: at,
	}, "", palette, 120)
	if !strings.Contains(info, "\x1b[") || !strings.Contains(errLine, "\x1b[") {
		t.Fatalf("expected colorized event lines:\ninfo=%q\nerror=%q", info, errLine)
	}
	infoColumn := visibleColumnOfSubstring(info, "info-message")
	errorColumn := visibleColumnOfSubstring(errLine, "error-message")
	if infoColumn < 0 || errorColumn < 0 {
		t.Fatalf("messages missing from rendered lines:\ninfo=%q\nerror=%q", info, errLine)
	}
	if infoColumn != errorColumn {
		t.Fatalf("colored event message columns differ: info=%d error=%d\ninfo=%q\nerror=%q", infoColumn, errorColumn, info, errLine)
	}

	sidebar := renderDevConsoleSidebar(devConsoleSnapshot{
		Selected: "all",
		Sources: []devConsoleSource{
			{Source: devdash.DevSource{ID: "api", URL: "info-detail"}, Status: "ok"},
			{Source: devdash.DevSource{ID: "api", URL: "error-detail"}, Status: "ok", ErrorCount: 2},
		},
	}, palette, 80, 3)
	if len(sidebar) != 3 {
		t.Fatalf("sidebar rendered %d lines, want 3: %#v", len(sidebar), sidebar)
	}
	infoDetailColumn := visibleColumnOfSubstring(sidebar[1], "info-detail")
	errorDetailColumn := visibleColumnOfSubstring(sidebar[2], "error-detail")
	if infoDetailColumn < 0 || errorDetailColumn < 0 {
		t.Fatalf("details missing from sidebar lines: %#v", sidebar)
	}
	if infoDetailColumn != errorDetailColumn {
		t.Fatalf("colored sidebar detail columns differ: info=%d error=%d\ninfo=%q\nerror=%q", infoDetailColumn, errorDetailColumn, sidebar[1], sidebar[2])
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
			{ID: "worker:durable", Kind: "worker", Name: "durable", PID: "12351", Status: "running"},
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

func visibleColumnOfSubstring(line, needle string) int {
	idx := strings.Index(line, needle)
	if idx < 0 {
		return -1
	}
	return visibleStringWidth(line[:idx])
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
	state.handleKey(consoleKey{Kind: consoleKeyIgnored})
	if !state.searching || state.search != "b" {
		t.Fatalf("ignored key changed search state: searching:%v search:%q", state.searching, state.search)
	}
	state.handleKey(consoleKey{Kind: consoleKeyEsc})
	if state.searching || state.search != "" {
		t.Fatalf("escape did not cancel search: searching:%v search:%q", state.searching, state.search)
	}
}

func TestReadConsoleKeyParsesEscapeSequences(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReader(strings.NewReader("\x1b[A\x1b[5~\x1b[6~\x1b[<64;20;10M\x1b[<65;20;10M\x1b[<0;20;10M\x1b[<0;20;10m\x1bf"))
	wants := []consoleKeyKind{
		consoleKeyUp,
		consoleKeyPageUp,
		consoleKeyPageDown,
		consoleKeyMouseWheelUp,
		consoleKeyMouseWheelDown,
		consoleKeyIgnored,
		consoleKeyIgnored,
		consoleKeyIgnored,
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

func TestDevConsoleRefreshUsesVictoriaLogsFilters(t *testing.T) {
	t.Parallel()

	stack := installLogsVictoriaStack(t,
		devdash.DevEvent{ID: 1, AppID: "logsapp", SessionID: "session-a", Source: devdash.DevSource{ID: "api", Kind: "app"}, Level: "info", Message: "ok", CreatedAt: time.Now().UTC()},
		devdash.DevEvent{ID: 2, AppID: "logsapp", SessionID: "session-a", Source: devdash.DevSource{ID: "worker:durable", Kind: "worker"}, Level: "error", Message: "boom", CreatedAt: time.Now().UTC()},
	)
	state := devConsoleState{
		opts:      logsOptions{Limit: 10},
		appID:     "logsapp",
		sessionID: "session-a",
		selected:  "worker:durable",
		errors:    true,
	}

	if err := state.refresh(context.Background(), stack); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(state.events) != 1 || state.events[0].Message != "boom" {
		t.Fatalf("state events = %+v", state.events)
	}
}
