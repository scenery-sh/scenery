package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/termstyle"
)

func renderDevConsole(snapshot devConsoleSnapshot) string {
	return renderDevConsoleStyled(snapshot, termstyle.Palette{})
}

func renderDevConsoleStyled(snapshot devConsoleSnapshot, palette termstyle.Palette) string {
	size := normalizeTerminalSize(terminalSize{Width: snapshot.Width, Height: snapshot.Height})
	width := size.Width
	height := size.Height
	selected := snapshot.Selected
	if selected == "" {
		selected = "all"
	}
	lines := make([]string, 0, height)
	headerTitle := palette.Bold("scenery console")
	target := firstNonEmpty(snapshot.AppName, snapshot.AppRoot, "dev runtime")
	header := headerTitle + "  " + palette.Cyan(target)
	if snapshot.SessionID != "" {
		header += "  " + palette.Dim("session "+snapshot.SessionID)
	}
	lines = append(lines, fitStyledLine(header, width))

	viewTitle := selected
	if viewTitle == "all" {
		viewTitle = "all sources"
	}
	if snapshot.ErrorsOnly {
		viewTitle += " errors"
	}
	if snapshot.Search != "" {
		viewTitle += ` / "` + snapshot.Search + `"`
	}
	if snapshot.Frozen {
		viewTitle += "  [frozen]"
	}
	if snapshot.Searching {
		viewTitle += "  [search]"
	}
	lines = append(lines, fitStyledLine(palette.Dim("view")+" "+viewTitle, width))

	if width >= 96 {
		lines = append(lines, renderDevConsoleWide(snapshot, palette, width, height-len(lines)-1)...)
	} else {
		lines = append(lines, renderDevConsoleNarrow(snapshot, palette, width, height-len(lines)-1)...)
	}

	status := "tab source  arrows/page scroll  / search  f freeze  enter json  q quit"
	if snapshot.Frozen {
		status = "frozen  " + status
	}
	if snapshot.Scroll > 0 {
		status = fmt.Sprintf("scrollback %d  %s", snapshot.Scroll, status)
	}
	lines = append(lines, fitStyledLine(palette.Dim(status), width))
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func renderDevConsoleWide(snapshot devConsoleSnapshot, palette termstyle.Palette, width, available int) []string {
	if available <= 0 {
		return nil
	}
	sidebarWidth := width / 3
	if sidebarWidth < 30 {
		sidebarWidth = 30
	}
	if sidebarWidth > 46 {
		sidebarWidth = 46
	}
	logWidth := width - sidebarWidth - 1
	logRows := maxInt(1, available-1)
	left := renderDevConsoleSidebar(snapshot, palette, sidebarWidth, logRows)
	right := renderDevConsoleLogLines(snapshot, palette, logWidth, logRows)
	lines := []string{fitStyledLine(palette.Dim(strings.Repeat("-", sidebarWidth))+" "+palette.Dim(strings.Repeat("-", logWidth)), width)}
	for i := 0; i < logRows; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, padStyledLine(l, sidebarWidth)+" "+fitStyledLine(r, logWidth))
	}
	return lines
}

func renderDevConsoleNarrow(snapshot devConsoleSnapshot, palette termstyle.Palette, width, available int) []string {
	if available <= 0 {
		return nil
	}
	lines := []string{fitStyledLine(renderDevConsoleTabs(snapshot, palette), width)}
	sourceRows := minInt(3, maxInt(0, available/4))
	lines = append(lines, renderDevConsoleSidebar(snapshot, palette, width, sourceRows)...)
	remaining := available - len(lines)
	if remaining > 0 {
		lines = append(lines, renderDevConsoleLogLines(snapshot, palette, width, remaining)...)
	}
	return lines
}

func renderDevConsoleSidebar(snapshot devConsoleSnapshot, palette termstyle.Palette, width, rows int) []string {
	if rows <= 0 {
		return nil
	}
	lines := make([]string, 0, rows)
	lines = append(lines, fitStyledLine(palette.Bold("sources")+" "+palette.Dim(fmt.Sprintf("%d", len(snapshot.Sources))), width))
	for i, source := range snapshot.Sources {
		if len(lines) >= rows {
			break
		}
		marker := " "
		if source.Source.ID == snapshot.Selected {
			marker = ">"
		}
		if snapshot.Selected == "all" && i == 0 {
			marker = " "
		}
		label := source.Source.ID
		if i < 9 {
			label = fmt.Sprintf("%d %s", i+1, label)
		}
		status := firstNonEmpty(source.Status, "active")
		if source.ErrorCount > 0 {
			status = palette.Red(fmt.Sprintf("%s %d err", status, source.ErrorCount))
		} else {
			status = palette.Green(status)
		}
		detail := firstNonEmpty(source.Source.Reason, source.LastError, source.Source.URL, source.Source.Role)
		line := fmt.Sprintf("%s %s %s %s", marker, padStyled(label, 18), padStyled(status, 12), palette.Dim(detail))
		lines = append(lines, fitStyledLine(line, width))
	}
	if len(snapshot.Errors) > 0 && len(lines) < rows {
		lines = append(lines, fitStyledLine(palette.Bold("errors"), width))
	}
	for _, group := range snapshot.Errors {
		if len(lines) >= rows {
			break
		}
		last := "never"
		if !group.Last.IsZero() {
			last = humanDuration(time.Since(group.Last)) + " ago"
		}
		line := fmt.Sprintf("%s %dx %s %s", palette.Red(group.Source), group.Count, group.Message, palette.Dim(last))
		lines = append(lines, fitStyledLine(line, width))
	}
	return lines
}

func renderDevConsoleTabs(snapshot devConsoleSnapshot, palette termstyle.Palette) string {
	selected := firstNonEmpty(snapshot.Selected, "all")
	parts := []string{tabLabel("all", selected == "all")}
	for i, source := range snapshot.Sources {
		label := source.Source.ID
		if i < 9 {
			label = fmt.Sprintf("%d:%s", i+1, label)
		}
		parts = append(parts, tabLabel(label, selected == source.Source.ID))
	}
	return palette.Dim("sources ") + strings.Join(parts, " ")
}

func renderDevConsoleLogLines(snapshot devConsoleSnapshot, palette termstyle.Palette, width, rows int) []string {
	if rows <= 0 {
		return nil
	}
	eventRows := rows
	if snapshot.Expanded && len(snapshot.Events) > 0 {
		eventRows = maxInt(1, rows/2)
	}
	events := visibleDevEvents(snapshot.Events, eventRows, snapshot.Scroll)
	lines := make([]string, 0, rows)
	for _, event := range events {
		lines = append(lines, renderDevConsoleEventLine(event, snapshot.Search, palette, width))
	}
	if snapshot.Expanded && len(snapshot.Events) > 0 {
		last := snapshot.Events[len(snapshot.Events)-1]
		data, _ := json.MarshalIndent(devConsoleEventJSON(snapshot.AppName, snapshot.AppRoot, last), "", "  ")
		if len(lines) < rows {
			lines = append(lines, fitStyledLine(palette.Bold("event json"), width))
		}
		for _, line := range strings.Split(string(data), "\n") {
			if len(lines) >= rows {
				break
			}
			lines = append(lines, fitStyledLine(palette.Dim(line), width))
		}
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

func renderDevConsoleEventLine(event devdash.DevEvent, search string, palette termstyle.Palette, width int) string {
	timestamp := "--:--:--"
	if !event.CreatedAt.IsZero() {
		timestamp = event.CreatedAt.Local().Format("15:04:05.000")
	}
	source := firstNonEmpty(event.Source.ID, "process")
	level := strings.ToUpper(firstNonEmpty(event.Level, "info"))
	message := firstNonEmpty(event.Message, event.Raw)
	fields := compactJSONFields(event.Fields)
	if fields != "" {
		message += " " + palette.Dim(fields)
	}
	message = highlightSearch(message, search, palette)
	line := fmt.Sprintf("%s %s %s %s", palette.Dim(timestamp), padStyled(styleConsoleLevel(level, palette), 5), padStyled(palette.Cyan(source), 18), message)
	return fitStyledLine(line, width)
}

func styleConsoleLevel(level string, palette termstyle.Palette) string {
	switch strings.ToLower(level) {
	case "error", "fatal":
		return palette.Red(level)
	case "warn", "warning":
		return palette.Yellow(level)
	case "debug", "trace":
		return palette.Dim(level)
	default:
		return palette.Green(level)
	}
}

func highlightSearch(text, search string, palette termstyle.Palette) string {
	search = strings.TrimSpace(search)
	if search == "" || !palette.Enabled() {
		return text
	}
	lower := strings.ToLower(text)
	needle := strings.ToLower(search)
	idx := strings.Index(lower, needle)
	if idx < 0 {
		return text
	}
	return text[:idx] + palette.Inverse(text[idx:idx+len(search)]) + text[idx+len(search):]
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
		cliPayloadIdentity: newCLIPayloadIdentity(devdash.DevEventKind),
		ID:                 event.ID,
		Time:               event.CreatedAt.UTC().Format(time.RFC3339Nano),
		SessionID:          event.SessionID,
		Source:             event.Source,
		Level:              event.Level,
		Message:            event.Message,
		Raw:                event.Raw,
		Parse:              event.Parse,
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
	case "worker:durable":
		return 30
	case "database":
		return 40
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

func visibleDevEvents(events []devdash.DevEvent, rows, scroll int) []devdash.DevEvent {
	if rows <= 0 || len(events) == 0 {
		return nil
	}
	if len(events) <= rows {
		return events
	}
	end := len(events) - scroll
	if end > len(events) {
		end = len(events)
	}
	if end < 0 {
		end = 0
	}
	start := end - rows
	if start < 0 {
		start = 0
	}
	return events[start:end]
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

var enterRawTerminal = defaultEnterRawTerminal

func defaultEnterRawTerminal(stdin *os.File) (func(), error) {
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func normalizeTerminalSize(size terminalSize) terminalSize {
	if size.Width <= 0 {
		size.Width = defaultConsoleWidth
	}
	if size.Height <= 0 {
		size.Height = defaultConsoleHeight
	}
	if size.Width < minConsoleWidth {
		size.Width = minConsoleWidth
	}
	if size.Height < minConsoleHeight {
		size.Height = minConsoleHeight
	}
	return size
}
