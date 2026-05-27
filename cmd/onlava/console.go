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

	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/termstyle"
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
	MCP       string
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
	status := c.palette.Green("Done!")
	icon := c.palette.Green("✔")
	if err != nil {
		status = c.palette.Red("Failed")
		icon = c.palette.Red("✖")
	}
	c.printf(c.out, "  %s %s... %s %s\n", icon, title, status, c.palette.Dim(msLabel(time.Since(started))))
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
		c.printf(c.out, "    changed: %s\n", path)
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
		c.printf(c.err, "  Development Dashboard URL: %s\n", urls.Dashboard)
	}
	c.printf(c.err, "  onlava dev is still running and will rebuild after file changes.\n\n")
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
	c.printf(c.out, "\n  %s\n\n", c.palette.Bold("onlava development server running!"))
	width := len("Development Dashboard URL:")
	if len("Frontend URL:") > width {
		width = len("Frontend URL:")
	}
	for _, label := range []string{"Temporal UI URL:", "Grafana URL:"} {
		if len(label) > width {
			width = len(label)
		}
	}
	if c.verbose && len("VictoriaMetrics URL:") > width {
		width = len("VictoriaMetrics URL:")
	}
	c.printf(c.out, "  %-*s  %s\n", width, "Your API is running at:", urls.API)
	c.printf(c.out, "  %-*s  %s\n", width, "Development Dashboard URL:", urls.Dashboard)
	c.printf(c.out, "  %-*s  %s\n", width, "MCP SSE URL:", urls.MCP)
	for _, name := range sortedKeys(urls.Frontends) {
		c.printf(c.out, "  %-*s  %s\n", width, frontendLabel(name), urls.Frontends[name])
	}
	if urls.Grafana != nil && urls.Grafana.URL != "" {
		c.printf(c.out, "  %-*s  %s\n", width, "Grafana URL:", urls.Grafana.URL)
	}
	if urls.Temporal != "" {
		c.printf(c.out, "  %-*s  %s\n", width, "Temporal UI URL:", urls.Temporal)
	}
	if c.verbose {
		for _, item := range []struct {
			label string
			key   string
		}{
			{label: "VictoriaMetrics URL:", key: "metrics"},
			{label: "VictoriaLogs URL:", key: "logs"},
			{label: "VictoriaTraces URL:", key: "traces"},
		} {
			if url := urls.Victoria[item.key]; url != "" {
				c.printf(c.out, "  %-*s  %s\n", width, item.label, url)
			}
		}
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
	c.printf(c.out, "  %s %s... %s\n", c.palette.Green("✔"), title, c.palette.Green("Done!"))
}

func runURLData(urls runURLs, verbose bool) map[string]any {
	data := map[string]any{
		"api_url":       urls.API,
		"dashboard_url": urls.Dashboard,
		"mcp_url":       urls.MCP,
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
		return "Frontend URL:"
	}
	return "Frontend " + name + " URL:"
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
	header := c.palette.Red("ERR")
	c.printf(c.err, "\n  %s %s\n", header, label)
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		c.printf(c.err, "  %s\n", line)
	}
	c.printf(c.err, "\n")
}

func (c *runConsole) printf(w io.Writer, format string, args ...any) {
	if c.json {
		return
	}
	_, _ = fmt.Fprintf(w, format, args...)
}

func msLabel(duration time.Duration) string {
	return fmt.Sprintf("duration_ms=%d", duration.Milliseconds())
}

func (c *runConsole) Event(eventType string, data map[string]any) {
	if c == nil {
		return
	}
	if !c.json {
		return
	}
	event := runEvent{
		SchemaVersion: "onlava.run.event.v1",
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
