package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"pulse.dev/internal/termstyle"
)

type runConsole struct {
	out     io.Writer
	err     io.Writer
	palette termstyle.Palette
}

func newRunConsole(out, err io.Writer) *runConsole {
	return &runConsole{
		out:     out,
		err:     err,
		palette: termstyle.New(out),
	}
}

func (c *runConsole) Phase(title string, fn func() error) error {
	started := time.Now()
	err := fn()
	status := c.palette.Green("Done!")
	icon := c.palette.Green("✔")
	if err != nil {
		status = c.palette.Red("Failed")
		icon = c.palette.Red("✖")
	}
	c.printf(c.out, "  %s %s... %s %s\n", icon, title, status, c.palette.Dim(msLabel(time.Since(started))))
	return err
}

func (c *runConsole) RebuildDetected() {
	c.printf(c.out, "\n  %s\n\n", c.palette.Bold("Changes detected. Rebuilding..."))
}

func (c *runConsole) InitialBuildFailed(err error) {
	c.printError("initial build failed", err)
}

func (c *runConsole) RebuildFailed(err error) {
	c.printError("rebuild failed", err)
}

func (c *runConsole) Banner(apiURL, dashboardURL, mcpURL string) {
	c.printf(c.out, "\n  %s\n\n", c.palette.Bold("Pulse development server running!"))
	width := len("Development Dashboard URL:")
	c.printf(c.out, "  %-*s  %s\n", width, "Your API is running at:", apiURL)
	c.printf(c.out, "  %-*s  %s\n", width, "Development Dashboard URL:", dashboardURL)
	c.printf(c.out, "  %-*s  %s\n\n", width, "MCP SSE URL:", mcpURL)
}

func (c *runConsole) printError(label string, err error) {
	if err == nil {
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
	_, _ = fmt.Fprintf(w, format, args...)
}

func msLabel(duration time.Duration) string {
	return fmt.Sprintf("ms=%d", duration.Milliseconds())
}
