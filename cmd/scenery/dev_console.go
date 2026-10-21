package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/termstyle"
	"scenery.sh/internal/victoria"
)

const (
	defaultConsoleWidth     = 100
	defaultConsoleHeight    = 30
	minConsoleWidth         = 40
	minConsoleHeight        = 12
	devConsoleEventPoll     = 300 * time.Millisecond
	devConsoleHeartbeat     = 5 * time.Second
	devConsoleDefaultEvents = 300
)

type terminalSize struct {
	Width  int
	Height int
}

type devConsoleSnapshot struct {
	AppName    string
	AppRoot    string
	SessionID  string
	Sources    []devConsoleSource
	Events     []devdash.DevEvent
	Errors     []devConsoleErrorGroup
	Selected   string
	ErrorsOnly bool
	Search     string
	Searching  bool
	Frozen     bool
	Expanded   bool
	Scroll     int
	Width      int
	Height     int
}

type devConsoleSource struct {
	Source     devdash.DevSource
	Status     string
	EventCount int
	ErrorCount int
	LastLog    time.Time
	LastError  string
}

type devConsoleErrorGroup struct {
	Source  string
	Message string
	Count   int
	First   time.Time
	Last    time.Time
}

func runSceneryConsoleOrFallback(ctx context.Context, stdin *os.File, stdout io.Writer, opts logsOptions) error {
	if !isTerminal(stdin) || !writerIsTerminal(stdout) || strings.EqualFold(envpolicy.Get("TERM"), "dumb") || envpolicy.Get("CI") != "" {
		return runSceneryConsoleLogsFallback(ctx, stdout, opts)
	}
	return runSceneryConsole(ctx, stdin, stdout, opts)
}

func runSceneryConsoleLogsFallback(ctx context.Context, stdout io.Writer, opts logsOptions) error {
	return runSceneryLogsFunc(ctx, stdout, logArgsFromOptions(opts, true))
}

func runSceneryConsole(ctx context.Context, stdin *os.File, stdout io.Writer, opts logsOptions) (err error) {
	appRoot, appID, appName, err := discoverConsoleAppIdentity(opts.AppRoot)
	if err != nil {
		return err
	}
	sessionID, err := resolveLogsSessionID(ctx, opts.Session, appRoot)
	if err != nil {
		return err
	}
	victoria, err := logsVictoriaStack(ctx, appRoot)
	if err != nil {
		return err
	}

	restoreTerminal := func() {}
	terminalActive := false
	defer func() {
		if terminalActive {
			_, _ = fmt.Fprint(stdout, "\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[?1049l")
		}
		restoreTerminal()
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scenery console panic: %v", recovered)
		}
	}()
	if restore, rawErr := enterRawTerminal(stdin); rawErr == nil {
		restoreTerminal = restore
	} else {
		return runSceneryConsoleLogsFallback(ctx, stdout, opts)
	}
	_, _ = fmt.Fprint(stdout, "\x1b[?1049h\x1b[2J\x1b[H\x1b[?25l\x1b[?1000h\x1b[?1006h")
	terminalActive = true

	size := normalizeTerminalSize(getConsoleSize(stdin))
	state := devConsoleState{
		opts:      opts,
		appID:     appID,
		appName:   appName,
		appRoot:   appRoot,
		sessionID: sessionID,
		selected:  firstNonEmpty(opts.Source, "all"),
		width:     size.Width,
		height:    size.Height,
	}
	keyCh := make(chan consoleKey, 16)
	go readConsoleKeys(stdin, keyCh)
	resizeCh := notifyConsoleResize(ctx, stdin)

	pollTicker := time.NewTicker(devConsoleEventPoll)
	defer pollTicker.Stop()
	heartbeatTicker := time.NewTicker(devConsoleHeartbeat)
	defer heartbeatTicker.Stop()
	if err := state.refresh(ctx, victoria); err != nil {
		return err
	}
	renderer := newConsoleDiffRenderer(stdout, size)
	if err := renderer.Render(renderDevConsoleStyled(state.snapshot(), termstyle.New(stdout))); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case key, ok := <-keyCh:
			if !ok {
				return nil
			}
			if state.handleKey(key) {
				return nil
			}
			if err := state.refresh(ctx, victoria); err != nil {
				return err
			}
			if err := renderer.Render(renderDevConsoleStyled(state.snapshot(), termstyle.New(stdout))); err != nil {
				return err
			}
		case nextSize := <-resizeCh:
			nextSize = normalizeTerminalSize(nextSize)
			state.width = nextSize.Width
			state.height = nextSize.Height
			renderer.Resize(nextSize)
			if err := renderer.Render(renderDevConsoleStyled(state.snapshot(), termstyle.New(stdout))); err != nil {
				return err
			}
		case <-pollTicker.C:
			if !state.frozen {
				changed, err := state.refreshIfChanged(ctx, victoria)
				if err != nil {
					return err
				}
				if changed {
					if err := renderer.Render(renderDevConsoleStyled(state.snapshot(), termstyle.New(stdout))); err != nil {
						return err
					}
				}
			}
		case <-heartbeatTicker.C:
			if err := renderer.Render(renderDevConsoleStyled(state.snapshot(), termstyle.New(stdout))); err != nil {
				return err
			}
		}
	}
}

// Select the retained log identity once; desired config supplies only its
// display name, so malformed config cannot redirect or strand retained reads.
func discoverConsoleAppIdentity(option string) (root, appID, appName string, err error) {
	root, appID, err = discoverRuntimeAppIdentity(option)
	if err != nil {
		return "", "", "", err
	}
	appName = appID
	if data, readErr := os.ReadFile(app.ConfigPath(root)); readErr == nil {
		if cfg, configErr := app.ParseConfig(root, data); configErr == nil {
			appName = cfg.Name
		}
	}
	return root, appID, appName, nil
}

type devConsoleState struct {
	opts      logsOptions
	appID     string
	appName   string
	appRoot   string
	sessionID string
	selected  string
	errors    bool
	frozen    bool
	expanded  bool
	searching bool
	search    string
	scroll    int
	width     int
	height    int
	events    []devdash.DevEvent
	sources   []devConsoleSource
}

func (s *devConsoleState) refresh(ctx context.Context, victoria *victoria.Stack) error {
	query := logsDevEventQuery(s.opts, s.appID, s.sessionID)
	query.Limit = maxInt(s.opts.Limit, devConsoleDefaultEvents)
	if s.selected != "" && s.selected != "all" {
		query.SourceID = s.selected
	}
	if s.errors {
		query.Level = "error"
	}
	if s.search != "" {
		query.Grep = s.search
	}
	events, err := victoria.ListDevEvents(ctx, query)
	if err != nil {
		return err
	}
	sources, err := victoria.ListDevSources(ctx, s.appID, s.sessionID)
	if err != nil {
		return err
	}
	s.events = events
	s.sources = buildDevConsoleSources(sources, events)
	if s.selected != "all" && !devConsoleSourceExists(s.sources, s.selected) {
		s.selected = "all"
	}
	s.clampScroll()
	return nil
}

func (s *devConsoleState) refreshIfChanged(ctx context.Context, victoria *victoria.Stack) (bool, error) {
	before := s.signature()
	if err := s.refresh(ctx, victoria); err != nil {
		return false, err
	}
	return before != s.signature(), nil
}

func (s *devConsoleState) signature() string {
	var b strings.Builder
	fmt.Fprintf(&b, "selected=%s errors=%t search=%s count=%d sources=%d", s.selected, s.errors, s.search, len(s.events), len(s.sources))
	for _, event := range s.events {
		fmt.Fprintf(&b, "|%d/%s/%s", event.ID, event.Level, event.Source.ID)
	}
	for _, source := range s.sources {
		fmt.Fprintf(&b, "|%s/%s/%s/%d/%d", source.Source.ID, source.Status, source.Source.PID, source.EventCount, source.ErrorCount)
	}
	return b.String()
}

func (s *devConsoleState) snapshot() devConsoleSnapshot {
	return devConsoleSnapshot{
		AppName:    s.appName,
		AppRoot:    s.appRoot,
		SessionID:  s.sessionID,
		Sources:    append([]devConsoleSource(nil), s.sources...),
		Events:     append([]devdash.DevEvent(nil), s.events...),
		Errors:     buildDevConsoleErrorGroups(s.events),
		Selected:   s.selected,
		ErrorsOnly: s.errors,
		Search:     s.search,
		Searching:  s.searching,
		Frozen:     s.frozen,
		Expanded:   s.expanded,
		Scroll:     s.scroll,
		Width:      s.width,
		Height:     s.height,
	}
}

func (s *devConsoleState) handleKey(key consoleKey) bool {
	if s.searching {
		switch key.Kind {
		case consoleKeyEnter:
			s.searching = false
		case consoleKeyEsc:
			s.searching = false
			s.search = ""
			s.scroll = 0
		case consoleKeyBackspace:
			if len(s.search) > 0 {
				s.search = s.search[:len(s.search)-1]
			}
			s.scroll = 0
		case consoleKeyCtrlC:
			return true
		default:
			if key.Kind == consoleKeyRune && key.Rune >= 32 && key.Rune <= 126 {
				s.search += string(key.Rune)
				s.scroll = 0
			}
		}
		return false
	}
	switch key.Kind {
	case consoleKeyCtrlC:
		return true
	case consoleKeyTab:
		s.selected = nextDevConsoleSourceID(s.sources, s.selected)
		s.scroll = 0
	case consoleKeyEnter:
		s.expanded = !s.expanded
	case consoleKeyUp, consoleKeyMouseWheelUp:
		s.scrollBy(1)
	case consoleKeyDown, consoleKeyMouseWheelDown:
		s.scrollBy(-1)
	case consoleKeyPageUp:
		s.scrollBy(maxInt(5, s.visibleLogRows()-1))
	case consoleKeyPageDown:
		s.scrollBy(-maxInt(5, s.visibleLogRows()-1))
	case consoleKeyHome:
		s.scrollBy(len(s.events))
	case consoleKeyEnd:
		s.scroll = 0
	case consoleKeyCtrlL:
	default:
		if key.Kind != consoleKeyRune {
			return false
		}
		switch key.Rune {
		case 'q', 'Q':
			return true
		case 'a', 'A':
			s.selected = "all"
			s.scroll = 0
		case 'e', 'E':
			s.errors = !s.errors
			s.scroll = 0
		case 'f', 'F':
			s.frozen = !s.frozen
		case '/':
			s.searching = true
			s.search = ""
			s.scroll = 0
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			idx := int(key.Rune - '1')
			if idx >= 0 && idx < len(s.sources) {
				s.selected = s.sources[idx].Source.ID
				s.scroll = 0
			}
		}
	}
	s.clampScroll()
	return false
}

func (s *devConsoleState) scrollBy(delta int) {
	s.scroll += delta
	s.clampScroll()
}

func (s *devConsoleState) clampScroll() {
	maxScroll := len(s.events) - s.visibleLogRows()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
}

func (s *devConsoleState) visibleLogRows() int {
	height := normalizeTerminalSize(terminalSize{Width: s.width, Height: s.height}).Height
	if s.width >= 96 {
		return maxInt(3, height-6)
	}
	return maxInt(3, height-8)
}

func (s *devConsoleState) handleRune(key rune) bool {
	return s.handleKey(consoleKey{Kind: consoleKeyRune, Rune: key})
}

