package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
)

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
	Frozen     bool
	Expanded   bool
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

func runOnlavaConsoleOrFallback(ctx context.Context, stdin *os.File, stdout io.Writer, opts logsOptions) error {
	if opts.Session == "" {
		opts.Session = "current"
	}
	if !isTerminal(stdin) || !writerIsTerminal(stdout) || strings.EqualFold(os.Getenv("TERM"), "dumb") || os.Getenv("CI") != "" {
		return runOnlavaLogsFunc(ctx, stdout, logArgsFromOptions(opts, true))
	}
	return runOnlavaConsole(ctx, stdin, stdout, opts)
}

func runOnlavaConsole(ctx context.Context, stdin *os.File, stdout io.Writer, opts logsOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	sessionID, err := resolveLogsSessionID(ctx, opts.Session, appRoot)
	if err != nil {
		return err
	}
	store, err := openDevdashStore()
	if err != nil {
		return err
	}
	defer store.Close()

	restore, _ := enterRawTerminal(stdin)
	defer restore()
	fmt.Fprint(stdout, "\x1b[?1049h\x1b[2J\x1b[H")
	defer fmt.Fprint(stdout, "\x1b[?1049l")

	state := devConsoleState{
		opts:      opts,
		appID:     cfg.AppID(),
		appName:   cfg.Name,
		appRoot:   appRoot,
		sessionID: sessionID,
		selected:  firstNonEmpty(opts.Source, "all"),
	}
	keyCh := make(chan byte, 8)
	go readConsoleKeys(stdin, keyCh)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	if err := state.refresh(ctx, store); err != nil {
		return err
	}
	fmt.Fprint(stdout, "\x1b[H\x1b[2J"+renderDevConsole(state.snapshot()))
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
			if err := state.refresh(ctx, store); err != nil {
				return err
			}
			fmt.Fprint(stdout, "\x1b[H\x1b[2J"+renderDevConsole(state.snapshot()))
		case <-ticker.C:
			if !state.frozen {
				if err := state.refresh(ctx, store); err != nil {
					return err
				}
				fmt.Fprint(stdout, "\x1b[H\x1b[2J"+renderDevConsole(state.snapshot()))
			}
		}
	}
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
	events    []devdash.DevEvent
	sources   []devConsoleSource
}

func (s *devConsoleState) refresh(ctx context.Context, store *devdash.Store) error {
	query := logsDevEventQuery(s.opts, s.appID, s.sessionID)
	query.Limit = maxInt(s.opts.Limit, 300)
	if s.selected != "" && s.selected != "all" {
		query.SourceID = s.selected
	}
	if s.errors {
		query.Level = "error"
	}
	if s.search != "" {
		query.Grep = s.search
	}
	events, err := store.ListDevEvents(ctx, query)
	if err != nil {
		return err
	}
	sources, err := store.ListDevSources(ctx, s.appID, s.sessionID)
	if err != nil {
		return err
	}
	s.events = events
	s.sources = buildDevConsoleSources(sources, events)
	if s.selected != "all" && !devConsoleSourceExists(s.sources, s.selected) {
		s.selected = "all"
	}
	return nil
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
		Frozen:     s.frozen,
		Expanded:   s.expanded,
	}
}

func (s *devConsoleState) handleKey(key byte) bool {
	if s.searching {
		switch key {
		case '\r', '\n':
			s.searching = false
		case 27:
			s.searching = false
			s.search = ""
		case 127, '\b':
			if len(s.search) > 0 {
				s.search = s.search[:len(s.search)-1]
			}
		default:
			if key >= 32 && key <= 126 {
				s.search += string(key)
			}
		}
		return false
	}
	switch key {
	case 'q', 'Q':
		return true
	case '\t':
		s.selected = nextDevConsoleSourceID(s.sources, s.selected)
	case 'a', 'A':
		s.selected = "all"
	case 'e', 'E':
		s.errors = !s.errors
	case 'f', 'F':
		s.frozen = !s.frozen
	case '/':
		s.searching = true
		s.search = ""
	case '\r', '\n':
		s.expanded = !s.expanded
	case 12:
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		idx := int(key - '1')
		if idx >= 0 && idx < len(s.sources) {
			s.selected = s.sources[idx].Source.ID
		}
	}
	return false
}

func renderDevConsole(snapshot devConsoleSnapshot) string {
	var b strings.Builder
	selected := snapshot.Selected
	if selected == "" {
		selected = "all"
	}
	fmt.Fprintf(&b, "onlava dev session: %s / %s\n", firstNonEmpty(snapshot.AppName, snapshot.AppRoot), firstNonEmpty(snapshot.SessionID, "current"))
	fmt.Fprintf(&b, "[%s]", tabLabel("all", selected == "all"))
	for i, source := range snapshot.Sources {
		label := source.Source.ID
		if i < 9 {
			label = fmt.Sprintf("%d:%s", i+1, label)
		}
		fmt.Fprintf(&b, " [%s]", tabLabel(label, selected == source.Source.ID))
	}
	b.WriteString("\n")
	for _, source := range snapshot.Sources {
		pid := firstNonEmpty(source.Source.PID, "shared")
		last := "never"
		if !source.LastLog.IsZero() {
			last = humanDuration(time.Since(source.LastLog)) + " ago"
		}
		detail := firstNonEmpty(source.Source.Reason, source.LastError, source.Source.URL)
		if detail == "" {
			detail = source.Source.Role
		}
		fmt.Fprintf(&b, "%-20s %-9s pid %-8s %3d errors   last log %-8s %s\n",
			source.Source.ID, firstNonEmpty(source.Status, "active"), pid, source.ErrorCount, last, detail)
	}
	if len(snapshot.Errors) > 0 {
		b.WriteString("---------------- errors ----------------\n")
		for _, group := range snapshot.Errors {
			last := "never"
			if !group.Last.IsZero() {
				last = humanDuration(time.Since(group.Last)) + " ago"
			}
			fmt.Fprintf(&b, "[%s] %dx  %s  last %s\n", group.Source, group.Count, group.Message, last)
		}
	}
	title := selected
	if title == "all" {
		title = "all sources"
	}
	if snapshot.ErrorsOnly {
		title += " errors"
	}
	if snapshot.Search != "" {
		title += ` / "` + snapshot.Search + `"`
	}
	fmt.Fprintf(&b, "---------------- logs: %s ----------------\n", title)
	for _, event := range tailDevEvents(snapshot.Events, 18) {
		timestamp := "--:--:--"
		if !event.CreatedAt.IsZero() {
			timestamp = event.CreatedAt.Local().Format("15:04:05.000")
		}
		source := event.Source.ID
		if source == "" {
			source = "process"
		}
		fields := compactJSONFields(event.Fields)
		if fields != "" {
			fields = " " + fields
		}
		fmt.Fprintf(&b, "%s %-5s %-18s %s%s\n", timestamp, strings.ToUpper(event.Level), source, firstNonEmpty(event.Message, event.Raw), fields)
	}
	if snapshot.Expanded && len(snapshot.Events) > 0 {
		last := snapshot.Events[len(snapshot.Events)-1]
		data, _ := json.MarshalIndent(devConsoleEventJSON(snapshot.AppName, snapshot.AppRoot, last), "", "  ")
		fmt.Fprintf(&b, "---------------- event json ----------------\n%s\n", data)
	}
	status := "tab source  1..9 jump  a all  e errors  / search  f freeze  enter json  ctrl-l clear  q quit"
	if snapshot.Frozen {
		status = "frozen  " + status
	}
	fmt.Fprintf(&b, "---------------- %s\n", status)
	return b.String()
}

func buildDevConsoleErrorGroups(events []devdash.DevEvent) []devConsoleErrorGroup {
	byKey := map[string]*devConsoleErrorGroup{}
	for _, event := range events {
		if event.Level != "error" && event.Level != "fatal" {
			continue
		}
		source := firstNonEmpty(event.Source.ID, "process")
		message := firstNonEmpty(event.Message, event.Raw)
		key := source + "\x00" + message
		group := byKey[key]
		if group == nil {
			copy := devConsoleErrorGroup{Source: source, Message: message, First: event.CreatedAt}
			byKey[key] = &copy
			group = &copy
		}
		group.Count++
		if group.First.IsZero() || event.CreatedAt.Before(group.First) {
			group.First = event.CreatedAt
		}
		if event.CreatedAt.After(group.Last) {
			group.Last = event.CreatedAt
		}
	}
	groups := make([]devConsoleErrorGroup, 0, len(byKey))
	for _, group := range byKey {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count == groups[j].Count {
			return groups[i].Last.After(groups[j].Last)
		}
		return groups[i].Count > groups[j].Count
	})
	if len(groups) > 8 {
		groups = groups[:8]
	}
	return groups
}

func devConsoleEventJSON(appName, appRoot string, event devdash.DevEvent) logsEvent {
	out := logsEvent{
		SchemaVersion: devdash.DevEventSchemaVersion,
		ID:            event.ID,
		Time:          event.CreatedAt.UTC().Format(time.RFC3339Nano),
		SessionID:     event.SessionID,
		Source:        event.Source,
		Level:         event.Level,
		Message:       event.Message,
		Raw:           event.Raw,
		Parse:         event.Parse,
	}
	if len(event.Fields) > 0 && string(event.Fields) != "{}" {
		out.Fields = event.Fields
	}
	out.App.ID = appName
	out.App.Name = appName
	out.App.Root = appRoot
	return out
}

func buildDevConsoleSources(sources []devdash.DevSource, events []devdash.DevEvent) []devConsoleSource {
	byID := map[string]*devConsoleSource{}
	for _, source := range sources {
		sourceID := source.ID
		if sourceID == "" {
			continue
		}
		copy := devConsoleSource{Source: source, Status: source.Status}
		byID[sourceID] = &copy
	}
	for _, event := range events {
		source := event.Source
		if source.ID == "" {
			source.ID = "process"
		}
		item := byID[source.ID]
		if item == nil {
			copy := devConsoleSource{Source: source, Status: source.Status}
			byID[source.ID] = &copy
			item = &copy
		}
		if source.Status != "" {
			item.Status = source.Status
		}
		if source.PID != "" {
			item.Source.PID = source.PID
		}
		if source.URL != "" {
			item.Source.URL = source.URL
		}
		item.EventCount++
		if event.CreatedAt.After(item.LastLog) {
			item.LastLog = event.CreatedAt
		}
		if event.Level == "error" || event.Level == "fatal" {
			item.ErrorCount++
			item.LastError = firstNonEmpty(event.Message, event.Raw)
		}
	}
	out := make([]devConsoleSource, 0, len(byID))
	for _, item := range byID {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		return devConsoleSourceRank(out[i].Source.ID) < devConsoleSourceRank(out[j].Source.ID) ||
			(devConsoleSourceRank(out[i].Source.ID) == devConsoleSourceRank(out[j].Source.ID) && out[i].Source.ID < out[j].Source.ID)
	})
	return out
}

func devConsoleSourceRank(id string) int {
	switch id {
	case "api":
		return 10
	case "worker:go":
		return 20
	case "worker:typescript":
		return 21
	case "temporal":
		return 30
	case "postgres":
		return 40
	case "electric":
		return 41
	case "grafana":
		return 50
	case "build":
		return 80
	case "supervisor":
		return 90
	default:
		if strings.HasPrefix(id, "frontend:") {
			return 45
		}
		if strings.HasPrefix(id, "victoria") {
			return 51
		}
		return 60
	}
}

func nextDevConsoleSourceID(sources []devConsoleSource, selected string) string {
	if len(sources) == 0 {
		return "all"
	}
	if selected == "" || selected == "all" {
		return sources[0].Source.ID
	}
	for i, source := range sources {
		if source.Source.ID == selected {
			if i+1 < len(sources) {
				return sources[i+1].Source.ID
			}
			return "all"
		}
	}
	return "all"
}

func devConsoleSourceExists(sources []devConsoleSource, id string) bool {
	for _, source := range sources {
		if source.Source.ID == id {
			return true
		}
	}
	return false
}

func tailDevEvents(events []devdash.DevEvent, limit int) []devdash.DevEvent {
	if len(events) <= limit {
		return events
	}
	return events[len(events)-limit:]
}

func tabLabel(value string, active bool) string {
	if active {
		return "*" + value + "*"
	}
	return value
}

func compactJSONFields(fields json.RawMessage) string {
	if len(fields) == 0 || string(fields) == "{}" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal(fields, &values); err != nil || len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, values[key]))
	}
	return strings.Join(parts, " ")
}

func readConsoleKeys(stdin *os.File, keys chan<- byte) {
	var buf [1]byte
	for {
		n, err := stdin.Read(buf[:])
		if n > 0 {
			keys <- buf[0]
		}
		if err != nil {
			close(keys)
			return
		}
	}
}

func enterRawTerminal(stdin *os.File) (func(), error) {
	if !isTerminal(stdin) {
		return func() {}, nil
	}
	query := exec.Command("stty", "-g")
	query.Stdin = stdin
	state, err := query.Output()
	if err != nil {
		return func() {}, err
	}
	cmd := exec.Command("stty", "raw", "-echo")
	cmd.Stdin = stdin
	if err := cmd.Run(); err != nil {
		return func() {}, err
	}
	return func() {
		restore := exec.Command("stty", strings.TrimSpace(string(state)))
		restore.Stdin = stdin
		_ = restore.Run()
	}, nil
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func writerIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && isTerminal(file)
}

func humanDuration(value time.Duration) string {
	if value < time.Second {
		return "0s"
	}
	if value < time.Minute {
		return fmt.Sprintf("%ds", int(value.Seconds()))
	}
	if value < time.Hour {
		return fmt.Sprintf("%dm", int(value.Minutes()))
	}
	return fmt.Sprintf("%dh", int(value.Hours()))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
