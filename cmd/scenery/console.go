package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/termstyle"
)

type runConsole struct {
	out     io.Writer
	err     io.Writer
	verbose bool
	json    bool
	palette termstyle.Palette
	appName string
	appRoot string
	mu      sync.Mutex
}

type runURLs struct {
	API       string
	Dashboard string
	Frontends map[string]string
	Temporal  string
	Victoria  map[string]string
	Grafana   *devdash.GrafanaState
}

type runEvent struct {
	SchemaVersion string         `json:"schema_version"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	App           runEventApp    `json:"app"`
	Data          map[string]any `json:"data,omitempty"`
}

type runEventApp struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

func newRunConsole(out, err io.Writer, verbose, jsonMode bool, appName, appRoot string) *runConsole {
	return &runConsole{
		out:     out,
		err:     err,
		verbose: verbose,
		json:    jsonMode,
		palette: termstyle.New(out),
		appName: appName,
		appRoot: appRoot,
	}
}

func (c *runConsole) Phase(title string, fn func() error) error {
	started := time.Now()
	if c.json {
		c.Event("phase.start", map[string]any{
			"title": title,
		})
	}
	err := fn()
	if c.json {
		data := map[string]any{
			"title":       title,
			"ok":          err == nil,
			"duration_ms": time.Since(started).Milliseconds(),
		}
		if err != nil {
			data["error"] = err.Error()
		}
		c.Event("phase.finish", data)
		return err
	}
	icon := c.palette.Green("✔")
	if err != nil {
		icon = c.palette.Red("✖")
	}
	c.printf(c.out, "  %s %s %s\n", icon, title, c.palette.Dim("("+formatDuration(time.Since(started))+")"))
	return err
}

func (c *runConsole) RebuildDetected(paths []string) {
	if c.json {
		c.Event("rebuild.detected", map[string]any{
			"paths": append([]string(nil), paths...),
		})
		return
	}
	c.printf(c.out, "\n  %s\n", c.palette.Bold("Changes detected. Rebuilding..."))
	if !c.verbose || len(paths) == 0 {
		c.printf(c.out, "\n")
		return
	}
	for _, path := range paths {
		c.printf(c.out, "    %s\n", c.palette.Dim("changed: "+path))
	}
	c.printf(c.out, "\n")
}

func (c *runConsole) InitialBuildFailed(err error, urls runURLs) {
	if c.json && err != nil {
		c.Event("build.error", map[string]any{
			"stage": "initial",
			"error": err.Error(),
		})
		data := runURLData(urls, c.verbose)
		data["stage"] = "initial"
		data["error"] = err.Error()
		c.Event("run.failed", data)
		return
	}
	c.printError("initial build failed", err)
	if err == nil {
		return
	}
	if urls.Dashboard != "" {
		c.printf(c.err, "  %s %s %s\n", c.palette.Cyan("➜"), "Dashboard:", c.palette.Cyan(urls.Dashboard))
	}
	c.printf(c.err, "  %s\n\n", c.palette.Dim("scenery up is still running and will rebuild after file changes."))
}

func (c *runConsole) RebuildFailed(err error) {
	if c.json && err != nil {
		c.Event("build.error", map[string]any{
			"stage": "rebuild",
			"error": err.Error(),
		})
		return
	}
	c.printError("rebuild failed", err)
}

func (c *runConsole) Banner(urls runURLs) {
	if c.json {
		c.Event("run.ready", runURLData(urls, c.verbose))
		return
	}
	type bannerRow struct {
		label string
		url   string
	}
	rows := []bannerRow{
		{label: "API:", url: urls.API},
		{label: "Dashboard:", url: urls.Dashboard},
	}
	for _, name := range sortedKeys(urls.Frontends) {
		rows = append(rows, bannerRow{label: frontendLabel(name), url: urls.Frontends[name]})
	}
	if urls.Grafana != nil && urls.Grafana.URL != "" {
		rows = append(rows, bannerRow{label: "Grafana:", url: urls.Grafana.URL})
	}
	if urls.Temporal != "" {
		rows = append(rows, bannerRow{label: "Temporal UI:", url: urls.Temporal})
	}
	if c.verbose {
		for _, item := range []bannerRow{
			{label: "VictoriaMetrics:", url: urls.Victoria["metrics"]},
			{label: "VictoriaLogs:", url: urls.Victoria["logs"]},
			{label: "VictoriaTraces:", url: urls.Victoria["traces"]},
		} {
			if item.url != "" {
				rows = append(rows, item)
			}
		}
	}
	width := 0
	for _, row := range rows {
		if len(row.label) > width {
			width = len(row.label)
		}
	}
	c.printf(c.out, "\n  %s\n\n", c.palette.Bold("scenery development server running"))
	for _, row := range rows {
		c.printf(c.out, "  %s %-*s %s\n", c.palette.Cyan("➜"), width, row.label, c.palette.Cyan(row.url))
	}
	c.printf(c.out, "\n")
}

func (c *runConsole) SetupOutput(line, stream string) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if line == "" {
		return
	}
	if c == nil {
		return
	}
	if c.json {
		c.Event("setup.output", map[string]any{
			"line":   line,
			"stream": stream,
		})
		return
	}
	normalized := strings.TrimSpace(strings.TrimPrefix(line, "==>"))
	lower := strings.ToLower(normalized)
	switch {
	case strings.HasPrefix(lower, "atlas target:"):
		c.printSetupDetail("Atlas target", strings.TrimSpace(strings.TrimPrefix(normalized, "Atlas target:")))
	case strings.HasPrefix(lower, "atlas dry-run:"):
		c.printSetupDetail("Atlas dry-run", strings.TrimSpace(strings.TrimPrefix(normalized, "Atlas dry-run:")))
	case normalized == "Schema is synced, no changes to be made":
		c.printSetupDone("Atlas schema synced")
	case normalized == "No database changes needed":
		c.printSetupDone("No database changes needed")
	default:
		c.printf(c.out, "    %s\n", line)
	}
}

func (c *runConsole) printSetupDetail(label, value string) {
	if value == "" {
		c.printf(c.out, "  %s %s\n", c.palette.Cyan("•"), label)
		return
	}
	c.printf(c.out, "  %s %s: %s\n", c.palette.Cyan("•"), label, c.palette.Dim(value))
}

func (c *runConsole) printSetupDone(title string) {
	c.printf(c.out, "  %s %s\n", c.palette.Green("✔"), title)
}

func runURLData(urls runURLs, verbose bool) map[string]any {
	data := map[string]any{
		"api_url":       urls.API,
		"dashboard_url": urls.Dashboard,
		"frontend_urls": urls.Frontends,
	}
	if urls.Temporal != "" {
		data["temporal_url"] = urls.Temporal
	}
	if verbose {
		data["victoria_urls"] = urls.Victoria
	}
	if urls.Grafana != nil {
		data["grafana"] = urls.Grafana
		if urls.Grafana.URL != "" {
			data["grafana_url"] = urls.Grafana.URL
		}
	}
	return data
}

func sortedKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func frontendLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Frontend:"
	}
	return "Frontend " + name + ":"
}

func (c *runConsole) printError(label string, err error) {
	if err == nil {
		return
	}
	if c.json {
		c.Event("error", map[string]any{
			"label": label,
			"error": err.Error(),
		})
		return
	}
	c.printf(c.err, "\n  %s %s\n", c.palette.Red("✖"), c.palette.Bold(label))
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		c.printf(c.err, "    %s\n", c.formatErrorLine(line))
	}
	c.printf(c.err, "\n")
}

// formatErrorLine highlights the leading "path:line:" position in diagnostics
// so individual findings stand out from their messages.
func (c *runConsole) formatErrorLine(line string) string {
	position, message, ok := splitDiagnosticPosition(line)
	if !ok {
		return line
	}
	return c.palette.Cyan(position) + c.palette.Dim(":") + message
}

func splitDiagnosticPosition(line string) (string, string, bool) {
	index := strings.Index(line, ": ")
	if index <= 0 {
		return "", "", false
	}
	position := line[:index]
	if strings.ContainsAny(position, " \t") {
		return "", "", false
	}
	return position, line[index+1:], true
}

func (c *runConsole) printf(w io.Writer, format string, args ...any) {
	if c.json {
		return
	}
	_, _ = fmt.Fprintf(w, format, args...)
}

func formatDuration(duration time.Duration) string {
	switch {
	case duration < time.Second:
		return fmt.Sprintf("%dms", duration.Milliseconds())
	case duration < time.Minute:
		return fmt.Sprintf("%.1fs", duration.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(duration.Minutes()), int(duration.Seconds())%60)
	}
}

func (c *runConsole) Event(eventType string, data map[string]any) {
	if c == nil {
		return
	}
	if !c.json {
		return
	}
	event := runEvent{
		SchemaVersion: "scenery.run.event.v1",
		Type:          eventType,
		Time:          time.Now().UTC().Format(time.RFC3339Nano),
		App: runEventApp{
			Name: c.appName,
			Root: c.appRoot,
		},
		Data: data,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	enc := json.NewEncoder(c.out)
	_ = enc.Encode(event)
}

type setupOutputWriter struct {
	console  *runConsole
	stream   string
	fallback io.Writer
	mu       sync.Mutex
	buf      []byte
}

func newSetupOutputWriter(console *runConsole, stream string, fallback io.Writer) *setupOutputWriter {
	return &setupOutputWriter{
		console:  console,
		stream:   stream,
		fallback: fallback,
	}
}

func (w *setupOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := len(p)
	for len(p) > 0 {
		index := bytes.IndexByte(p, '\n')
		if index < 0 {
			w.buf = append(w.buf, p...)
			return total, nil
		}
		line := append(w.buf, p[:index]...)
		w.buf = w.buf[:0]
		w.emit(line)
		p = p[index+1:]
	}
	return total, nil
}

func (w *setupOutputWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return
	}
	w.emit(w.buf)
	w.buf = nil
}

func (w *setupOutputWriter) emit(line []byte) {
	if w.console == nil {
		if w.fallback != nil {
			_, _ = fmt.Fprintln(w.fallback, string(line))
		}
		return
	}
	w.console.SetupOutput(string(line), w.stream)
}
