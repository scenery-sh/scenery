package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/inspect"
)

type harnessUIOptions struct {
	AppRoot      string
	DashboardURL string
	JSON         bool
	Write        bool
	Headed       bool
}

type harnessUIResponse struct {
	SchemaVersion string            `json:"schema_version"`
	OK            bool              `json:"ok"`
	GeneratedAt   string            `json:"generated_at"`
	App           inspect.AppRef    `json:"app"`
	DashboardURL  string            `json:"dashboard_url"`
	Routes        []harnessUIRoute  `json:"routes"`
	Artifacts     []harnessArtifact `json:"artifacts"`
	Diagnostics   []checkDiagnostic `json:"diagnostics,omitempty"`
	NextActions   []string          `json:"next_actions,omitempty"`
	Wrote         string            `json:"wrote,omitempty"`
}

type harnessUIRoute struct {
	Name            string                    `json:"name"`
	URL             string                    `json:"url"`
	OK              bool                      `json:"ok"`
	DurationMS      int64                     `json:"duration_ms"`
	Markers         []harnessUIMarker         `json:"markers"`
	Screenshot      string                    `json:"screenshot,omitempty"`
	ConsoleErrors   []harnessUIConsoleMessage `json:"console_errors,omitempty"`
	NetworkFailures []harnessUINetworkFailure `json:"network_failures,omitempty"`
	Error           string                    `json:"error,omitempty"`
}

type harnessUIMarker struct {
	Selector string `json:"selector"`
	Count    int    `json:"count"`
	Found    bool   `json:"found"`
}

type harnessUIConsoleMessage struct {
	Route   string `json:"route,omitempty"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type harnessUINetworkFailure struct {
	Route string `json:"route,omitempty"`
	URL   string `json:"url,omitempty"`
	Type  string `json:"type,omitempty"`
	Error string `json:"error"`
}

type harnessUIRouteSpec struct {
	Name    string
	Path    string
	Markers []string
}

type harnessUIBrowserResult struct {
	Routes          []harnessUIRoute
	ConsoleErrors   []harnessUIConsoleMessage
	NetworkFailures []harnessUINetworkFailure
	Artifacts       []harnessArtifact
}

type harnessUIDevProcess struct {
	cmd          *exec.Cmd
	dashboardURL string
	done         chan error
	output       *safeLineTail
}

var runHarnessUIBrowserChecksFunc = runHarnessUIBrowserChecks

func runOnlavaHarnessUI(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseHarnessUIArgs(args)
	if err != nil {
		return err
	}
	if !opts.JSON {
		return fmt.Errorf("onlava harness ui currently requires --json")
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}

	resp := harnessUIResponse{
		SchemaVersion: "onlava.harness.ui.v1",
		OK:            true,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		App: inspect.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".onlava.json"),
		},
	}

	dashboardURL := strings.TrimSpace(opts.DashboardURL)
	var dev *harnessUIDevProcess
	if dashboardURL == "" {
		dev, err = startHarnessUIDevProcess(ctx, appRoot)
		if err != nil {
			resp.OK = false
			resp.Diagnostics = append(resp.Diagnostics, checkDiagnostic{
				Stage:           "browser ui harness",
				Severity:        "error",
				Message:         err.Error(),
				SuggestedAction: "Run `onlava dev --json` for the app or pass --dashboard-url to an existing dashboard.",
			})
			return finishHarnessUI(stdout, appRoot, opts, resp)
		}
		defer dev.Stop()
		dashboardURL = dev.dashboardURL
	}
	resp.DashboardURL = dashboardURL

	artifactRoot := filepath.Join(appRoot, ".onlava", "harness", "ui")
	routes := buildHarnessUIRoutes(appDashboardURL(dashboardURL, cfg.AppID()))
	result, err := runHarnessUIBrowserChecksFunc(ctx, routes, artifactRoot, opts.Headed)
	if err != nil {
		resp.OK = false
		resp.Diagnostics = append(resp.Diagnostics, checkDiagnostic{
			Stage:           "browser ui harness",
			Severity:        "error",
			Message:         err.Error(),
			SuggestedAction: "Install Chrome/Chromium or rerun with a reachable dashboard URL.",
		})
	}
	resp.Routes = result.Routes
	resp.Artifacts = result.Artifacts
	for _, route := range resp.Routes {
		if !route.OK {
			resp.OK = false
		}
		for _, item := range route.ConsoleErrors {
			resp.Diagnostics = append(resp.Diagnostics, checkDiagnostic{
				Stage:           "browser ui harness",
				Severity:        "error",
				Message:         fmt.Sprintf("%s console error: %s", route.Name, item.Message),
				SuggestedAction: "Open the screenshot and console artifact for the failing route.",
			})
		}
		for _, item := range route.NetworkFailures {
			resp.Diagnostics = append(resp.Diagnostics, checkDiagnostic{
				Stage:           "browser ui harness",
				Severity:        "error",
				Message:         fmt.Sprintf("%s network failure: %s", route.Name, item.Error),
				SuggestedAction: "Check the dashboard server and route-specific network artifact.",
			})
		}
		if route.Error != "" {
			resp.Diagnostics = append(resp.Diagnostics, checkDiagnostic{
				Stage:           "browser ui harness",
				Severity:        "error",
				Message:         route.Error,
				SuggestedAction: "Open the route screenshot and fix the missing DOM marker or render error.",
			})
		}
	}
	for _, item := range resp.Diagnostics {
		if item.Severity == "error" {
			resp.OK = false
			break
		}
	}
	resp.NextActions = buildHarnessUINextActions(resp)
	if opts.Write {
		resp.Wrote = filepath.Join(appRoot, ".onlava", "harness", "ui", "latest.json")
		if err := writeHarnessUIResult(resp.Wrote, resp); err != nil {
			return err
		}
		resp.Artifacts = markHarnessUIArtifacts(resp.Artifacts, resp.Wrote)
	}
	if err := writeHarnessUIJSON(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return &silentCLIError{err: fmt.Errorf("onlava harness ui failed")}
	}
	return nil
}

func parseHarnessUIArgs(args []string) (harnessUIOptions, error) {
	opts := harnessUIOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return harnessUIOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--dashboard-url":
			i++
			if i >= len(args) {
				return harnessUIOptions{}, fmt.Errorf("missing value for --dashboard-url")
			}
			opts.DashboardURL = args[i]
		case "--json":
			opts.JSON = true
		case "--write":
			opts.Write = true
		case "--headed":
			opts.Headed = true
		default:
			return harnessUIOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func startHarnessUIDevProcess(ctx context.Context, appRoot string) (*harnessUIDevProcess, error) {
	appAddr, err := freeLoopbackAddr()
	if err != nil {
		return nil, err
	}
	dashboardAddr, err := freeLoopbackAddr()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe, "dev", "--app-root", appRoot, "--listen", appAddr, "--json")
	cmd.Dir = appRoot
	cmd.Env = append(os.Environ(),
		"ONLAVA_DEV_DASHBOARD_ADDR="+dashboardAddr,
		"ONLAVA_DEV_VICTORIA=0",
		"ONLAVA_DEV_VICTORIA_DOWNLOAD=0",
		"ONLAVA_LOCAL_PROXY=0",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	proc := &harnessUIDevProcess{
		cmd:          cmd,
		dashboardURL: "http://" + dashboardAddr,
		done:         make(chan error, 1),
		output:       &safeLineTail{limit: 80},
	}
	ready := make(chan harnessUIDevSignal, 1)
	go proc.scanDevOutput(stdout, ready)
	go proc.scanDevOutput(stderr, nil)
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()
	if err := proc.waitReady(ctx, ready); err != nil {
		proc.Stop()
		return nil, err
	}
	return proc, nil
}

type harnessUIDevSignal struct {
	err error
}

func (p *harnessUIDevProcess) scanDevOutput(r io.Reader, ready chan<- harnessUIDevSignal) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		p.output.Add(line)
		if ready == nil {
			continue
		}
		var event runEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		switch event.Type {
		case "run.ready":
			select {
			case ready <- harnessUIDevSignal{}:
			default:
			}
		case "run.failed", "build.error", "process.compile-error":
			select {
			case ready <- harnessUIDevSignal{err: fmt.Errorf("onlava dev failed before dashboard was ready: %s", runEventError(event))}:
			default:
			}
		}
	}
}

func (p *harnessUIDevProcess) waitReady(ctx context.Context, ready <-chan harnessUIDevSignal) error {
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-p.done:
			return fmt.Errorf("onlava dev exited before dashboard was ready: %v\n%s", err, p.output.String())
		case signal := <-ready:
			if signal.err != nil {
				return fmt.Errorf("%w\n%s", signal.err, p.output.String())
			}
			if err := waitForHTTP(ctx, p.dashboardURL, 10*time.Second); err != nil {
				return fmt.Errorf("dashboard did not become reachable: %w\n%s", err, p.output.String())
			}
			return nil
		case <-timer.C:
			return fmt.Errorf("timed out waiting for onlava dev readiness\n%s", p.output.String())
		}
	}
}

func runEventError(event runEvent) string {
	if value, ok := event.Data["error"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	data, err := json.Marshal(event.Data)
	if err != nil || len(data) == 0 {
		return event.Type
	}
	return string(data)
}

func (p *harnessUIDevProcess) Stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
	case <-timer.C:
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func runHarnessUIBrowserChecks(ctx context.Context, routes []harnessUIRouteSpec, artifactRoot string, headed bool) (harnessUIBrowserResult, error) {
	return runCDPBrowserChecks(ctx, routes, artifactRoot, headed)
}

func buildHarnessUIRoutes(appURL string) []harnessUIRouteSpec {
	return []harnessUIRouteSpec{
		{Name: "dashboard-home", Path: appURL, Markers: []string{`[data-onlava-ui="AppShell"]`}},
		{Name: "api-explorer", Path: joinDashboardPath(appURL, "requests"), Markers: []string{`[data-onlava-ui="AppShell"]`}},
		{Name: "service-catalog", Path: joinDashboardPath(appURL, "envs/local/api"), Markers: []string{`[data-onlava-ui="AppShell"]`}},
		{Name: "traces", Path: joinDashboardPath(appURL, "envs/local/traces"), Markers: []string{`[data-onlava-ui="AppShell"]`}},
		{Name: "data-explorer", Path: joinDashboardPath(appURL, "data"), Markers: []string{`[data-onlava-ui="DataExplorerLayout"]`}},
		{Name: "db-explorer", Path: joinDashboardPath(appURL, "db"), Markers: []string{`[data-onlava-ui="AppShell"]`}},
	}
}

func appDashboardURL(rawURL, appID string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return strings.TrimRight(rawURL, "/") + "/" + url.PathEscape(appID)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		parsed.Path = "/" + url.PathEscape(appID)
		return parsed.String()
	}
	return parsed.String()
}

func joinDashboardPath(appURL, suffix string) string {
	parsed, err := url.Parse(appURL)
	if err != nil {
		return strings.TrimRight(appURL, "/") + "/" + strings.TrimLeft(suffix, "/")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	return parsed.String()
}

func finishHarnessUI(stdout io.Writer, appRoot string, opts harnessUIOptions, resp harnessUIResponse) error {
	resp.NextActions = buildHarnessUINextActions(resp)
	if opts.Write {
		resp.Wrote = filepath.Join(appRoot, ".onlava", "harness", "ui", "latest.json")
		if err := writeHarnessUIResult(resp.Wrote, resp); err != nil {
			return err
		}
	}
	if err := writeHarnessUIJSON(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return &silentCLIError{err: fmt.Errorf("onlava harness ui failed")}
	}
	return nil
}

func buildHarnessUINextActions(resp harnessUIResponse) []string {
	if resp.OK {
		return nil
	}
	actions := []string{}
	if len(resp.Diagnostics) > 0 {
		actions = append(actions, "Open `.onlava/harness/ui/console.jsonl`, `.onlava/harness/ui/network.jsonl`, and the route screenshot for the first failing route.")
	}
	for _, route := range resp.Routes {
		if !route.OK {
			actions = append(actions, "Fix dashboard route `"+route.Name+"`, then rerun `onlava harness ui --json`.")
			break
		}
	}
	return actions
}

func markHarnessUIArtifacts(items []harnessArtifact, latest string) []harnessArtifact {
	items = append([]harnessArtifact(nil), items...)
	items = append(items, harnessArtifact{
		Name:          "ui-harness",
		Path:          ".onlava/harness/ui/latest.json",
		SchemaVersion: "onlava.harness.ui.v1",
		Exists:        pathExists(latest),
	})
	return items
}

func writeHarnessUIResult(path string, resp harnessUIResponse) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func writeHarnessUIJSON(w io.Writer, payload harnessUIResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func waitForHTTP(ctx context.Context, rawURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("status %s", resp.Status)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastErr)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func freeLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

type safeLineTail struct {
	mu    sync.Mutex
	limit int
	lines []string
}

func (t *safeLineTail) Add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if t.limit > 0 && len(t.lines) > t.limit {
		t.lines = append([]string(nil), t.lines[len(t.lines)-t.limit:]...)
	}
}

func (t *safeLineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
