package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pbrazdil/onlava/internal/envpolicy"
)

func runCDPBrowserChecks(ctx context.Context, routes []harnessUIRouteSpec, artifactRoot string, headed bool) (harnessUIBrowserResult, error) {
	if err := os.MkdirAll(filepath.Join(artifactRoot, "screenshots"), 0o755); err != nil {
		return harnessUIBrowserResult{}, err
	}
	if err := os.MkdirAll(filepath.Join(artifactRoot, "dom"), 0o755); err != nil {
		return harnessUIBrowserResult{}, err
	}
	browserCtx, cancel := context.WithTimeout(ctx, time.Duration(len(routes))*25*time.Second)
	defer cancel()

	browser, err := startHarnessBrowser(browserCtx, headed)
	if err != nil {
		return harnessUIBrowserResult{}, err
	}
	defer browser.Close()

	client, err := browser.newPage(browserCtx)
	if err != nil {
		return harnessUIBrowserResult{}, err
	}
	defer client.Close()

	observer := newHarnessRouteObserver()
	client.handleEvent = observer.handle
	for _, method := range []string{"Runtime.enable", "Network.enable", "Page.enable"} {
		if err := client.call(browserCtx, method, nil, nil); err != nil {
			return harnessUIBrowserResult{}, err
		}
	}

	result := harnessUIBrowserResult{}
	for _, spec := range routes {
		routeStarted := time.Now()
		consoleStart, networkStart := observer.startRoute(spec.Name)
		route := harnessUIRoute{Name: spec.Name, URL: spec.Path, OK: true}

		err := client.call(browserCtx, "Page.navigate", map[string]any{"url": spec.Path}, nil)
		if err == nil {
			err = waitForHarnessBrowserReady(browserCtx, client)
		}
		if err == nil {
			time.Sleep(250 * time.Millisecond)
			for _, selector := range spec.Markers {
				count, markerErr := harnessQuerySelectorCount(browserCtx, client, selector)
				route.Markers = append(route.Markers, harnessUIMarker{Selector: selector, Count: count, Found: count > 0})
				if markerErr != nil {
					err = markerErr
					break
				}
				if count == 0 {
					route.OK = false
					route.Error = fmt.Sprintf("missing required DOM marker %s", selector)
				}
			}
		}
		if err == nil {
			journeyResults, journeyErr := runHarnessUIJourney(browserCtx, client, spec)
			route.Journey = journeyResults
			if journeyErr != nil {
				err = journeyErr
			}
		}
		domPath := filepath.Join("dom", spec.Name+".json")
		if snapshot, domErr := harnessCaptureDOMSnapshot(browserCtx, client, spec.Name); domErr == nil {
			abs := filepath.Join(artifactRoot, domPath)
			if writeErr := writeHarnessUIDOMSnapshot(abs, snapshot); writeErr == nil {
				route.DOMSnapshot = filepath.ToSlash(filepath.Join(".onlava", "harness", "ui", domPath))
				result.Artifacts = append(result.Artifacts, harnessArtifact{Name: "dom:" + spec.Name, Path: route.DOMSnapshot, SchemaVersion: "onlava.harness.ui.dom.v1", Exists: true})
			}
		}
		screenshotPath := filepath.Join("screenshots", spec.Name+".png")
		if screenshot, shotErr := harnessCaptureScreenshot(browserCtx, client); shotErr == nil && len(screenshot) > 0 {
			abs := filepath.Join(artifactRoot, screenshotPath)
			if writeErr := os.WriteFile(abs, screenshot, 0o644); writeErr == nil {
				route.Screenshot = filepath.ToSlash(filepath.Join(".onlava", "harness", "ui", screenshotPath))
				result.Artifacts = append(result.Artifacts, harnessArtifact{Name: "screenshot:" + spec.Name, Path: route.Screenshot, Exists: true})
			}
		}
		if err != nil {
			route.OK = false
			route.Error = err.Error()
		}
		route.ConsoleErrors, route.NetworkFailures = observer.routeEvents(consoleStart, networkStart)
		if len(route.ConsoleErrors) > 0 || len(route.NetworkFailures) > 0 {
			route.OK = false
		}
		route.DurationMS = time.Since(routeStarted).Milliseconds()
		result.Routes = append(result.Routes, route)
	}
	result.ConsoleErrors, result.NetworkFailures = observer.allEvents()
	if err := writeHarnessBrowserJSONL(filepath.Join(artifactRoot, "console.jsonl"), result.ConsoleErrors); err == nil {
		result.Artifacts = append(result.Artifacts, harnessArtifact{Name: "console", Path: ".onlava/harness/ui/console.jsonl", Exists: true})
	}
	if err := writeHarnessBrowserJSONL(filepath.Join(artifactRoot, "network.jsonl"), result.NetworkFailures); err == nil {
		result.Artifacts = append(result.Artifacts, harnessArtifact{Name: "network", Path: ".onlava/harness/ui/network.jsonl", Exists: true})
	}
	return result, nil
}

func runHarnessUIJourney(ctx context.Context, client *harnessCDPClient, spec harnessUIRouteSpec) ([]harnessUIJourneyResult, error) {
	results := []harnessUIJourneyResult{}
	var failures []string
	for _, check := range spec.Checks {
		result, err := runHarnessUIJourneyCheck(ctx, client, check)
		results = append(results, result)
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	for _, action := range spec.Actions {
		result, err := runHarnessUIJourneyAction(ctx, client, action)
		results = append(results, result)
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return results, nil
}

func runHarnessUIJourneyCheck(ctx context.Context, client *harnessCDPClient, check harnessUIJourneyCheckSpec) (harnessUIJourneyResult, error) {
	required := check.Required
	result := harnessUIJourneyResult{
		Name:     check.Name,
		Kind:     "selector",
		Selector: check.Selector,
		Required: required,
	}
	if len(check.AnySelectors) > 0 {
		result.Kind = "any-selector"
		result.Selectors = append([]string(nil), check.AnySelectors...)
		if required {
			selector, count, err := waitForHarnessAnySelector(ctx, client, check.AnySelectors, 5*time.Second)
			if err != nil {
				result.Detail = err.Error()
				return result, fmt.Errorf("%s: %w", check.Name, err)
			}
			result.Selector = selector
			result.Count = count
			result.Found = true
			return result, nil
		}
		for _, selector := range check.AnySelectors {
			count, err := harnessQuerySelectorCount(ctx, client, selector)
			if err != nil {
				result.Detail = err.Error()
				if required {
					return result, fmt.Errorf("%s: %w", check.Name, err)
				}
				return result, nil
			}
			if count > 0 {
				result.Selector = selector
				result.Count = count
				result.Found = true
				return result, nil
			}
		}
		result.Detail = "none of the expected selectors were found"
		if required {
			return result, fmt.Errorf("%s: %s", check.Name, result.Detail)
		}
		return result, nil
	}
	if required {
		if err := waitForHarnessSelector(ctx, client, check.Selector, 5*time.Second); err != nil {
			result.Detail = err.Error()
			return result, fmt.Errorf("%s: %w", check.Name, err)
		}
	}
	count, err := harnessQuerySelectorCount(ctx, client, check.Selector)
	result.Count = count
	result.Found = count > 0
	if err != nil {
		result.Detail = err.Error()
		if required {
			return result, fmt.Errorf("%s: %w", check.Name, err)
		}
		return result, nil
	}
	if required && count == 0 {
		result.Detail = "selector was not found"
		return result, fmt.Errorf("%s: %s %s", check.Name, result.Detail, check.Selector)
	}
	return result, nil
}

func runHarnessUIJourneyAction(ctx context.Context, client *harnessCDPClient, action harnessUIJourneyActionSpec) (harnessUIJourneyResult, error) {
	result := harnessUIJourneyResult{
		Name:     action.Name,
		Kind:     "action",
		Selector: action.Click,
		Required: !action.Optional,
	}
	if action.WaitSelector != "" {
		waitCount, waitErr := harnessQuerySelectorCount(ctx, client, action.WaitSelector)
		if waitErr == nil && waitCount > 0 {
			result.Found = true
			result.Count = waitCount
			result.Selectors = []string{action.WaitSelector}
			result.Detail = "target already visible"
			return result, nil
		}
	}
	count, err := harnessQuerySelectorCount(ctx, client, action.Click)
	result.Count = count
	result.Found = count > 0
	if err != nil {
		result.Detail = err.Error()
		if action.Optional {
			result.Skipped = true
			return result, nil
		}
		return result, fmt.Errorf("%s: %w", action.Name, err)
	}
	if count == 0 {
		result.Detail = "action target was not found"
		if action.Optional {
			result.Skipped = true
			return result, nil
		}
		return result, fmt.Errorf("%s: %s %s", action.Name, result.Detail, action.Click)
	}
	if err := harnessClickSelector(ctx, client, action.Click); err != nil {
		result.Detail = err.Error()
		if action.Optional {
			result.Skipped = true
			return result, nil
		}
		return result, fmt.Errorf("%s: %w", action.Name, err)
	}
	if action.WaitSelector != "" {
		if err := waitForHarnessSelector(ctx, client, action.WaitSelector, 5*time.Second); err != nil {
			result.Detail = err.Error()
			if action.Optional {
				result.Skipped = true
				return result, nil
			}
			return result, fmt.Errorf("%s: %w", action.Name, err)
		}
		result.Selectors = []string{action.WaitSelector}
	}
	result.Found = true
	return result, nil
}

type harnessBrowserProcess struct {
	cmd      *exec.Cmd
	port     string
	profile  string
	output   bytes.Buffer
	stopOnce sync.Once
}

func startHarnessBrowser(ctx context.Context, headed bool) (*harnessBrowserProcess, error) {
	exe, err := harnessBrowserExecutable()
	if err != nil {
		return nil, err
	}
	port, err := harnessFreeTCPPort()
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "onlava-harness-browser-*")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + port,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--ignore-certificate-errors",
	}
	if !headed {
		args = append(args, "--headless=new", "--disable-gpu")
	}
	args = append(args, "about:blank")
	cmd := exec.CommandContext(ctx, exe, args...)
	browser := &harnessBrowserProcess{cmd: cmd, port: port, profile: profile}
	cmd.Stdout = &browser.output
	cmd.Stderr = &browser.output
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return nil, err
	}
	if err := browser.waitReady(ctx); err != nil {
		browser.Close()
		return nil, err
	}
	return browser, nil
}

func (b *harnessBrowserProcess) Close() {
	b.stopOnce.Do(func() {
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() {
				_ = b.cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = b.cmd.Process.Kill()
				<-done
			}
		}
		if b.profile != "" {
			_ = os.RemoveAll(b.profile)
		}
	})
}

func (b *harnessBrowserProcess) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+b.port+"/json/version", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("browser did not expose DevTools on 127.0.0.1:%s\n%s", b.port, b.output.String())
}

func (b *harnessBrowserProcess) newPage(ctx context.Context) (*harnessCDPClient, error) {
	endpoint := "http://127.0.0.1:" + b.port + "/json/new?" + url.QueryEscape("about:blank")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create browser target returned %s", resp.Status)
	}
	var target struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return nil, err
	}
	if target.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("browser target did not include webSocketDebuggerUrl")
	}
	return newHarnessCDPClient(ctx, target.WebSocketDebuggerURL)
}

func harnessBrowserExecutable() (string, error) {
	for _, env := range []string{"CHROME_BIN", "CHROMIUM_BIN"} {
		if value := strings.TrimSpace(envpolicy.Get(env)); value != "" {
			return value, nil
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "google-chrome-stable"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if goruntime.GOOS == "darwin" {
		for _, path := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			filepath.Join(envpolicy.Get("HOME"), "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		} {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("Chrome or Chromium executable not found; set CHROME_BIN")
}

func harnessFreeTCPPort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port), nil
}

type harnessCDPClient struct {
	conn        *websocket.Conn
	mu          sync.Mutex
	nextID      int
	pending     map[int]chan harnessCDPMessage
	handleEvent func(harnessCDPMessage)
	done        chan struct{}
	readErr     error
}

type harnessCDPMessage struct {
	ID     int              `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *harnessCDPError `json:"error,omitempty"`
}

type harnessCDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newHarnessCDPClient(ctx context.Context, endpoint string) (*harnessCDPClient, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := &harnessCDPClient{
		conn:    conn,
		pending: make(map[int]chan harnessCDPMessage),
		done:    make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

func (c *harnessCDPClient) Close() {
	_ = c.conn.Close()
	<-c.done
}

func (c *harnessCDPClient) readLoop() {
	defer close(c.done)
	for {
		var msg harnessCDPMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			c.mu.Lock()
			c.readErr = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
				close(ch)
			}
			continue
		}
		if c.handleEvent != nil {
			c.handleEvent(msg)
		}
	}
}

func (c *harnessCDPClient) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan harnessCDPMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	if err := c.conn.WriteJSON(payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			c.mu.Lock()
			err := c.readErr
			c.mu.Unlock()
			if err == nil {
				err = fmt.Errorf("browser connection closed")
			}
			return err
		}
		if msg.Error != nil {
			return fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		if out != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
}

type harnessRouteObserver struct {
	mu              sync.Mutex
	currentRoute    string
	requestURLs     map[string]string
	consoleErrors   []harnessUIConsoleMessage
	networkFailures []harnessUINetworkFailure
}

func newHarnessRouteObserver() *harnessRouteObserver {
	return &harnessRouteObserver{requestURLs: make(map[string]string)}
}

func (o *harnessRouteObserver) startRoute(name string) (int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.currentRoute = name
	return len(o.consoleErrors), len(o.networkFailures)
}

func (o *harnessRouteObserver) routeEvents(consoleStart, networkStart int) ([]harnessUIConsoleMessage, []harnessUINetworkFailure) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]harnessUIConsoleMessage(nil), o.consoleErrors[consoleStart:]...), append([]harnessUINetworkFailure(nil), o.networkFailures[networkStart:]...)
}

func (o *harnessRouteObserver) allEvents() ([]harnessUIConsoleMessage, []harnessUINetworkFailure) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]harnessUIConsoleMessage(nil), o.consoleErrors...), append([]harnessUINetworkFailure(nil), o.networkFailures...)
}

func (o *harnessRouteObserver) handle(msg harnessCDPMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch msg.Method {
	case "Network.requestWillBeSent":
		var params struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			o.requestURLs[params.RequestID] = params.Request.URL
		}
	case "Network.loadingFailed":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			ErrorText string `json:"errorText"`
			Canceled  bool   `json:"canceled"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && !params.Canceled {
			o.networkFailures = append(o.networkFailures, harnessUINetworkFailure{
				Route: o.currentRoute,
				URL:   o.requestURLs[params.RequestID],
				Type:  params.Type,
				Error: params.ErrorText,
			})
		}
	case "Runtime.consoleAPICalled":
		var params struct {
			Type string                   `json:"type"`
			Args []harnessRemoteCDPObject `json:"args"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.Type == "error" {
			o.consoleErrors = append(o.consoleErrors, harnessUIConsoleMessage{
				Route:   o.currentRoute,
				Level:   params.Type,
				Message: harnessRemoteObjectMessages(params.Args),
			})
		}
	case "Runtime.exceptionThrown":
		var params struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		message := "unhandled exception"
		if json.Unmarshal(msg.Params, &params) == nil {
			if params.ExceptionDetails.Text != "" {
				message = params.ExceptionDetails.Text
			}
			if params.ExceptionDetails.Exception.Description != "" {
				message = params.ExceptionDetails.Exception.Description
			}
		}
		o.consoleErrors = append(o.consoleErrors, harnessUIConsoleMessage{
			Route:   o.currentRoute,
			Level:   "exception",
			Message: message,
		})
	}
}

type harnessRemoteCDPObject struct {
	Type        string          `json:"type"`
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description"`
}

func waitForHarnessBrowserReady(ctx context.Context, client *harnessCDPClient) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var out harnessEvaluateResult
		err := client.call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    `document.readyState !== "loading" && !!document.body`,
			"returnByValue": true,
		}, &out)
		if err == nil && out.Result.ValueBool() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for document readiness")
}

func harnessQuerySelectorCount(ctx context.Context, client *harnessCDPClient, selector string) (int, error) {
	var out harnessEvaluateResult
	err := client.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf("document.querySelectorAll(%q).length", selector),
		"returnByValue": true,
	}, &out)
	if err != nil {
		return 0, err
	}
	return out.Result.ValueInt(), nil
}

func harnessClickSelector(ctx context.Context, client *harnessCDPClient, selector string) error {
	var out harnessEvaluateResult
	err := client.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": fmt.Sprintf(`(() => {
const el = document.querySelector(%q);
if (!el) return false;
el.scrollIntoView({ block: "center", inline: "center" });
el.click();
return true;
})()`, selector),
		"returnByValue": true,
	}, &out)
	if err != nil {
		return err
	}
	if !out.Result.ValueBool() {
		return fmt.Errorf("selector %s was not clickable", selector)
	}
	return nil
}

func waitForHarnessSelector(ctx context.Context, client *harnessCDPClient, selector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		count, err := harnessQuerySelectorCount(ctx, client, selector)
		if err == nil && count > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for selector %s", selector)
}

func waitForHarnessAnySelector(ctx context.Context, client *harnessCDPClient, selectors []string, timeout time.Duration) (string, int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, selector := range selectors {
			count, err := harnessQuerySelectorCount(ctx, client, selector)
			if err == nil && count > 0 {
				return selector, count, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", 0, fmt.Errorf("timed out waiting for any selector: %s", strings.Join(selectors, ", "))
}

type harnessUIDOMSnapshot struct {
	SchemaVersion string             `json:"schema_version"`
	Route         string             `json:"route"`
	URL           string             `json:"url"`
	Title         string             `json:"title,omitempty"`
	ReadyState    string             `json:"ready_state"`
	SemanticNodes []harnessUIDOMNode `json:"semantic_nodes"`
}

type harnessUIDOMNode struct {
	Marker string `json:"marker"`
	State  string `json:"state,omitempty"`
	Tag    string `json:"tag,omitempty"`
	Role   string `json:"role,omitempty"`
	Href   string `json:"href,omitempty"`
	Text   string `json:"text,omitempty"`
}

func harnessCaptureDOMSnapshot(ctx context.Context, client *harnessCDPClient, routeName string) (harnessUIDOMSnapshot, error) {
	var out harnessEvaluateResult
	err := client.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": `(() => {
const compact = (value) => String(value || "").replace(/\s+/g, " ").trim().slice(0, 300);
return {
  url: window.location.href,
  title: document.title,
  ready_state: document.readyState,
  semantic_nodes: Array.from(document.querySelectorAll("[data-onlava-ui]")).slice(0, 200).map((el) => ({
    marker: el.getAttribute("data-onlava-ui") || "",
    state: el.getAttribute("data-onlava-state") || "",
    tag: el.tagName ? el.tagName.toLowerCase() : "",
    role: el.getAttribute("role") || "",
    href: el.getAttribute("href") || "",
    text: compact(el.innerText || el.textContent || "")
  }))
};
})()`,
		"returnByValue": true,
	}, &out)
	if err != nil {
		return harnessUIDOMSnapshot{}, err
	}
	var snapshot harnessUIDOMSnapshot
	if err := json.Unmarshal(out.Result.Value, &snapshot); err != nil {
		return harnessUIDOMSnapshot{}, err
	}
	snapshot.SchemaVersion = "onlava.harness.ui.dom.v1"
	snapshot.Route = routeName
	if snapshot.SemanticNodes == nil {
		snapshot.SemanticNodes = []harnessUIDOMNode{}
	}
	return snapshot, nil
}

func writeHarnessUIDOMSnapshot(path string, snapshot harnessUIDOMSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

type harnessEvaluateResult struct {
	Result harnessRemoteCDPObject `json:"result"`
}

func (o harnessRemoteCDPObject) ValueBool() bool {
	var value bool
	_ = json.Unmarshal(o.Value, &value)
	return value
}

func (o harnessRemoteCDPObject) ValueInt() int {
	var value float64
	_ = json.Unmarshal(o.Value, &value)
	return int(value)
}

func harnessCaptureScreenshot(ctx context.Context, client *harnessCDPClient) ([]byte, error) {
	var out struct {
		Data string `json:"data"`
	}
	if err := client.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Data)
}

func writeHarnessBrowserJSONL[T any](path string, items []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func harnessRemoteObjectMessages(args []harnessRemoteCDPObject) string {
	parts := []string{}
	for _, arg := range args {
		if len(arg.Value) > 0 && string(arg.Value) != "null" {
			var value any
			if json.Unmarshal(arg.Value, &value) == nil {
				parts = append(parts, fmt.Sprint(value))
				continue
			}
			parts = append(parts, string(arg.Value))
			continue
		}
		if arg.Description != "" {
			parts = append(parts, arg.Description)
		}
	}
	return strings.Join(parts, " ")
}
