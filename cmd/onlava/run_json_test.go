package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestParseRunArgs(t *testing.T) {
	opts, err := parseRunArgs([]string{"--port", "4444", "--listen", "0.0.0.0", "--app-root", "/tmp/app", "--env", "production", "--log-format", "json"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if opts.Port != 4444 || opts.Listen != "0.0.0.0" || opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseRunArgsRejectsDevFlags(t *testing.T) {
	for _, flag := range []string{"--verbose", "--json", "--watch", "--dashboard", "--db-studio", "--proxy"} {
		if _, err := parseRunArgs([]string{flag}); err == nil {
			t.Fatalf("parseRunArgs(%q) returned nil error", flag)
		}
	}
}

func TestParseDevArgs(t *testing.T) {
	opts, err := parseDevArgs([]string{"--port", "4444", "--listen", "0.0.0.0", "--verbose", "--json", "--app-root", "/tmp/app", "--proxy", "--trust"})
	if err != nil {
		t.Fatalf("parseDevArgs returned error: %v", err)
	}
	if opts.Port != 4444 || opts.Listen != "0.0.0.0" || !opts.Verbose || !opts.JSON || opts.AppRoot != "/tmp/app" || !opts.Proxy || !opts.Trust {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestDevCommandUsesWatcherPath(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()

	called := false
	runWithWatchFunc = func(addr string, verbose, jsonMode bool, appRoot string) error {
		called = true
		if addr != "127.0.0.1:4444" || !verbose || !jsonMode || appRoot != "/tmp/app" {
			t.Fatalf("watch args = %q %v %v %q", addr, verbose, jsonMode, appRoot)
		}
		if got := getenvForTest("ONLAVA_LOCAL_PROXY"); got != "1" {
			t.Fatalf("ONLAVA_LOCAL_PROXY = %q, want 1", got)
		}
		if got := getenvForTest("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); got != "0" {
			t.Fatalf("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL = %q, want 0", got)
		}
		return nil
	}

	if err := devCommand([]string{"--port", "4444", "--verbose", "--json", "--app-root", "/tmp/app", "--proxy"}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandEnablesProxyByDefault(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()

	called := false
	runWithWatchFunc = func(addr string, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("ONLAVA_LOCAL_PROXY"); got != "1" {
			t.Fatalf("ONLAVA_LOCAL_PROXY = %q, want 1", got)
		}
		if got := getenvForTest("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); got != "0" {
			t.Fatalf("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL = %q, want 0", got)
		}
		return nil
	}

	if err := devCommand([]string{}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandRespectsProxyDisableEnv(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY", "0")

	called := false
	runWithWatchFunc = func(addr string, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("ONLAVA_LOCAL_PROXY"); got != "0" {
			t.Fatalf("ONLAVA_LOCAL_PROXY = %q, want 0", got)
		}
		return nil
	}

	if err := devCommand([]string{}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandProxyFlagOverridesDisableEnv(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY", "0")

	called := false
	runWithWatchFunc = func(addr string, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("ONLAVA_LOCAL_PROXY"); got != "1" {
			t.Fatalf("ONLAVA_LOCAL_PROXY = %q, want 1", got)
		}
		return nil
	}

	if err := devCommand([]string{"--proxy"}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandPreservesTrustSkipEnv(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", "1")

	called := false
	runWithWatchFunc = func(addr string, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); got != "1" {
			t.Fatalf("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL = %q, want 1", got)
		}
		return nil
	}

	if err := devCommand([]string{}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandTrustFlagOverridesTrustSkipEnv(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", "1")

	called := false
	runWithWatchFunc = func(addr string, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); got != "0" {
			t.Fatalf("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL = %q, want 0", got)
		}
		return nil
	}

	if err := devCommand([]string{"--trust"}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func getenvForTest(key string) string {
	return os.Getenv(key)
}

func TestRunCommandUsesHeadlessPath(t *testing.T) {
	prev := runHeadlessFunc
	defer func() { runHeadlessFunc = prev }()

	called := false
	runHeadlessFunc = func(addr string, opts runOptions) error {
		called = true
		if addr != "127.0.0.1:4444" || opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" {
			t.Fatalf("headless args = %q %+v", addr, opts)
		}
		return nil
	}

	if err := runCommand([]string{"--port", "4444", "--app-root", "/tmp/app", "--env", "production", "--log-format", "json"}); err != nil {
		t.Fatalf("runCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected headless path to be called")
	}
}

func TestRunConsoleJSONPhaseAndBanner(t *testing.T) {
	var out bytes.Buffer
	console := newRunConsole(&out, &out, false, true, "jsonapp", "/repo/jsonapp")

	if err := console.Phase("Compiling application source code", func() error { return nil }); err != nil {
		t.Fatalf("Phase() error = %v", err)
	}
	console.Banner(runURLs{
		API:       "https://api.jsonapp.localhost",
		Dashboard: "https://console.jsonapp.localhost/jsonapp",
		MCP:       "https://mcp.jsonapp.localhost/sse?appID=jsonapp",
		Frontend:  "https://onlava.jsonapp.localhost",
	})

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("line count = %d\n%s", len(lines), out.String())
	}

	var first struct {
		SchemaVersion string         `json:"schema_version"`
		Type          string         `json:"type"`
		App           map[string]any `json:"app"`
		Data          map[string]any `json:"data"`
	}
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("json.Unmarshal(first): %v\n%s", err, lines[0])
	}
	if first.SchemaVersion != "onlava.run.event.v1" || first.Type != "phase.start" {
		t.Fatalf("first event = %+v", first)
	}

	var second struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatalf("json.Unmarshal(second): %v\n%s", err, lines[1])
	}
	if second.Type != "phase.finish" || second.Data["ok"] != true {
		t.Fatalf("second event = %+v", second)
	}

	var third struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(lines[2], &third); err != nil {
		t.Fatalf("json.Unmarshal(third): %v\n%s", err, lines[2])
	}
	if third.Type != "run.ready" || third.Data["api_url"] != "https://api.jsonapp.localhost" {
		t.Fatalf("third event = %+v", third)
	}
	if _, ok := third.Data["victoria_urls"]; ok {
		t.Fatalf("third event unexpectedly included victoria URLs: %+v", third)
	}
}

func TestRunConsoleHidesVictoriaUnlessVerbose(t *testing.T) {
	urls := runURLs{
		API:       "https://api.jsonapp.localhost",
		Dashboard: "https://console.jsonapp.localhost/jsonapp",
		MCP:       "https://mcp.jsonapp.localhost/sse?appID=jsonapp",
		Victoria: map[string]string{
			"metrics": "http://127.0.0.1:8428",
			"logs":    "http://127.0.0.1:9428",
			"traces":  "http://127.0.0.1:10428",
		},
	}

	var quietOut bytes.Buffer
	quiet := newRunConsole(&quietOut, &quietOut, false, false, "jsonapp", "/repo/jsonapp")
	quiet.Banner(urls)
	if bytes.Contains(quietOut.Bytes(), []byte("Victoria")) || bytes.Contains(quietOut.Bytes(), []byte("8428")) {
		t.Fatalf("quiet banner included Victoria details:\n%s", quietOut.String())
	}

	var verboseOut bytes.Buffer
	verbose := newRunConsole(&verboseOut, &verboseOut, true, false, "jsonapp", "/repo/jsonapp")
	verbose.Banner(urls)
	if !bytes.Contains(verboseOut.Bytes(), []byte("VictoriaMetrics URL:")) || !bytes.Contains(verboseOut.Bytes(), []byte("http://127.0.0.1:8428")) {
		t.Fatalf("verbose banner missing Victoria details:\n%s", verboseOut.String())
	}
}

func TestRunConsoleJSONHidesVictoriaUnlessVerbose(t *testing.T) {
	urls := runURLs{
		API:       "https://api.jsonapp.localhost",
		Dashboard: "https://console.jsonapp.localhost/jsonapp",
		MCP:       "https://mcp.jsonapp.localhost/sse?appID=jsonapp",
		Victoria: map[string]string{
			"metrics": "http://127.0.0.1:8428",
		},
	}

	var quietOut bytes.Buffer
	quiet := newRunConsole(&quietOut, &quietOut, false, true, "jsonapp", "/repo/jsonapp")
	quiet.Banner(urls)
	var quietEvent struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(quietOut.Bytes()), &quietEvent); err != nil {
		t.Fatalf("json.Unmarshal(quiet): %v\n%s", err, quietOut.String())
	}
	if _, ok := quietEvent.Data["victoria_urls"]; ok {
		t.Fatalf("quiet JSON banner included Victoria URLs: %+v", quietEvent)
	}

	var verboseOut bytes.Buffer
	verbose := newRunConsole(&verboseOut, &verboseOut, true, true, "jsonapp", "/repo/jsonapp")
	verbose.Banner(urls)
	var verboseEvent struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(verboseOut.Bytes()), &verboseEvent); err != nil {
		t.Fatalf("json.Unmarshal(verbose): %v\n%s", err, verboseOut.String())
	}
	if _, ok := verboseEvent.Data["victoria_urls"]; !ok {
		t.Fatalf("verbose JSON banner missing Victoria URLs: %+v", verboseEvent)
	}
}
