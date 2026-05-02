package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"onlava.com/internal/termstyle"
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
	Frontend  string
	DBStudio  string
	Victoria  map[string]string
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

func (c *runConsole) InitialBuildFailed(err error) {
	if c.json && err != nil {
		c.Event("build.error", map[string]any{
			"stage": "initial",
			"error": err.Error(),
		})
		return
	}
	c.printError("initial build failed", err)
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
		c.Event("run.ready", map[string]any{
			"api_url":       urls.API,
			"dashboard_url": urls.Dashboard,
			"mcp_url":       urls.MCP,
			"frontend_url":  urls.Frontend,
			"db_studio_url": urls.DBStudio,
			"victoria_urls": urls.Victoria,
		})
		return
	}
	c.printf(c.out, "\n  %s\n\n", c.palette.Bold("Onlava development server running!"))
	width := len("Development Dashboard URL:")
	if len("Onlava App URL:") > width {
		width = len("Onlava App URL:")
	}
	if len("VictoriaMetrics URL:") > width {
		width = len("VictoriaMetrics URL:")
	}
	c.printf(c.out, "  %-*s  %s\n", width, "Your API is running at:", urls.API)
	c.printf(c.out, "  %-*s  %s\n", width, "Development Dashboard URL:", urls.Dashboard)
	c.printf(c.out, "  %-*s  %s\n", width, "MCP SSE URL:", urls.MCP)
	if urls.Frontend != "" {
		c.printf(c.out, "  %-*s  %s\n", width, "Onlava App URL:", urls.Frontend)
	}
	if urls.DBStudio != "" {
		c.printf(c.out, "  %-*s  %s\n", width, "Drizzle Studio URL:", urls.DBStudio)
	}
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
	c.printf(c.out, "\n")
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
