package pulse_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPulseRunBasicApp(t *testing.T) {
	repo := repoRoot(t)
	appDir := filepath.Join(repo, "testdata", "apps", "basic")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunEnv(dashAddr)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer func() {
		cancel()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for pulse process to exit")
		}
	}()

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")

	postJSON(t, "http://"+addr+"/echo/Alice?title=Dr", map[string]string{"body": "body"}, map[string]string{"X-Echo": "hdr"}, http.StatusOK, map[string]any{"message": "hi Alice Dr hdr body"})
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
	getJSON(t, "http://"+addr+"/service.CustomStatus", nil, http.StatusCreated, map[string]any{"message": "created"})
	getJSON(t, "http://"+addr+"/service.AuthEcho", map[string]string{"Authorization": "Bearer token123"}, http.StatusOK, map[string]any{"user": "user-1", "role": "admin"})
	getJSON(t, "http://"+addr+"/raw/alpha/beta", nil, http.StatusOK, map[string]any{"path": "alpha/beta", "method": "GET"})
	assertCORSPreflight(t, "http://"+addr+"/service.AuthEcho")
	assertCORSActual(t, "http://"+addr+"/service.AuthEcho")
}

func TestPulseRunReloadsOnGoChanges(t *testing.T) {
	repo := repoRoot(t)
	sourceAppDir := filepath.Join(repo, "testdata", "apps", "basic")
	appDir := filepath.Join(t.TempDir(), "basic")
	copyDir(t, sourceAppDir, appDir)
	rewriteFixtureReplace(t, filepath.Join(appDir, "go.mod"), repo)

	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunEnv(dashAddr)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})

	apiPath := filepath.Join(appDir, "service", "api.go")
	data, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), `Prefix: "hi"`, `Prefix: "bye"`, 1)
	if updated == string(data) {
		t.Fatal("failed to update test fixture source")
	}
	if err := os.WriteFile(apiPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	waitForJSONResponse(t, "http://"+addr+"/service.CallPrivate", http.StatusOK, map[string]any{"message": "secret:bye"})
}

func TestPulseRunLoadsSecretsFromDotEnv(t *testing.T) {
	repo := repoRoot(t)
	appDir := filepath.Join(repo, "testdata", "apps", "secrets")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunEnv(dashAddr)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/secrets")
	getJSON(t, "http://"+addr+"/secrets", nil, http.StatusOK, map[string]any{
		"service": "service-secret",
		"helper":  "helper-secret",
	})
}

func TestPulseRunMiddlewareApp(t *testing.T) {
	repo := repoRoot(t)
	appDir := filepath.Join(repo, "testdata", "apps", "middleware")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunEnv(dashAddr)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.Context")
	assertJSONResponseWithHeaders(t, mustRequest(t, http.MethodGet, "http://"+addr+"/service.Context", nil), http.StatusOK, map[string]any{"message": "svc"}, map[string]string{
		"X-Global-Middleware": "true",
	})
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "middleware:handler"})
	getJSON(t, "http://"+addr+"/service.Error", nil, http.StatusInternalServerError, map[string]any{"code": "internal", "message": "middleware error"})
	assertJSONResponseWithHeaders(t, mustRequest(t, http.MethodGet, "http://"+addr+"/raw/alpha", nil), http.StatusOK, map[string]any{"id": "alpha"}, map[string]string{
		"X-Raw-Middleware": "true",
	})
}

func TestPulseRunExecutesCronJobs(t *testing.T) {
	repo := repoRoot(t)
	appDir := filepath.Join(repo, "testdata", "apps", "cron")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunEnv(dashAddr)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/cron/status")
	waitForCronStatus(t, "http://"+addr+"/cron/status")
}

func TestPulseBuildProducesRunnableBinary(t *testing.T) {
	repo := repoRoot(t)
	appDir := filepath.Join(repo, "testdata", "apps", "basic")
	pulseBinary := buildPulseBinary(t, repo)
	outputPath := filepath.Join(t.TempDir(), "basic-app")

	buildCmd := exec.Command(pulseBinary, "build", "-o", outputPath)
	buildCmd.Dir = appDir
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pulse build failed: %v\n%s", err, buildOutput)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("built binary missing: %v", err)
	}

	port := freePort(t)
	addr := "127.0.0.1:" + port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, outputPath)
	cmd.Env = append(os.Environ(), "PULSE_LISTEN_ADDR="+addr, "PULSE_LOCAL_PROXY=0")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start built app: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
}

func TestPulseRunServesHTTPSHostnames(t *testing.T) {
	repo := repoRoot(t)
	sourceAppDir := filepath.Join(repo, "testdata", "apps", "basic")
	appDir := filepath.Join(t.TempDir(), "basic")
	copyDir(t, sourceAppDir, appDir)
	rewriteFixtureReplace(t, filepath.Join(appDir, "go.mod"), repo)
	writePulseApp(t, appDir, `{"name":"basicapp","proxy":{"workspace":"ignored","api_host":"api.onlv.localhost","console_host":"console.onlv.localhost","mcp_host":"mcp.onlv.localhost","frontend_host":"pulse.onlv.localhost"}}`)
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	httpPort := freePort(t)
	httpsPort := freePort(t)
	frontendPort := freePort(t)
	binary := buildPulseBinary(t, repo)

	frontendLn, err := net.Listen("tcp", "127.0.0.1:"+frontendPort)
	if err != nil {
		t.Fatal(err)
	}
	defer frontendLn.Close()
	frontendSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			_, _ = io.WriteString(w, "frontend ok")
		}),
	}
	defer frontendSrv.Close()
	go func() { _ = frontendSrv.Serve(frontendLn) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunProxyEnv(dashAddr, httpPort, httpsPort, "127.0.0.1:"+frontendPort)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")

	client := insecureHTTPSClient()
	apiURL := "https://api.onlv.localhost:" + httpsPort + "/service.CallPrivate"
	getJSONWithClient(t, client, apiURL, nil, http.StatusOK, map[string]any{"message": "secret:hi"})

	consoleURL := "https://console.onlv.localhost:" + httpsPort + "/"
	waitForURL(t, client, consoleURL)
	resp, err := client.Get(consoleURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("unexpected console status %d", resp.StatusCode)
	}

	mcpURL := "https://mcp.onlv.localhost:" + httpsPort + "/sse?app=basicapp"
	waitForURL(t, client, mcpURL)
	resp, err = client.Get(mcpURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected mcp status %d", resp.StatusCode)
	}

	frontendURL := "https://pulse.onlv.localhost:" + httpsPort + "/"
	waitForURL(t, client, frontendURL)
	resp, err = client.Get(frontendURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "frontend ok" {
		t.Fatalf("unexpected frontend response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestPulseBuiltBinaryServesHTTPSHostname(t *testing.T) {
	repo := repoRoot(t)
	sourceAppDir := filepath.Join(repo, "testdata", "apps", "basic")
	appDir := filepath.Join(t.TempDir(), "basic")
	copyDir(t, sourceAppDir, appDir)
	rewriteFixtureReplace(t, filepath.Join(appDir, "go.mod"), repo)
	writePulseApp(t, appDir, `{"name":"basicapp","proxy":{"api_host":"api.onlv.localhost","console_host":"console.onlv.localhost","mcp_host":"mcp.onlv.localhost","frontend_host":"pulse.onlv.localhost"}}`)
	pulseBinary := buildPulseBinary(t, repo)
	outputPath := filepath.Join(t.TempDir(), "basic-app")

	buildCmd := exec.Command(pulseBinary, "build", "-o", outputPath)
	buildCmd.Dir = appDir
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pulse build failed: %v\n%s", err, buildOutput)
	}

	port := freePort(t)
	addr := "127.0.0.1:" + port
	httpPort := freePort(t)
	httpsPort := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, outputPath)
	cmd.Env = append(
		os.Environ(),
		"PULSE_LISTEN_ADDR="+addr,
		"PULSE_LOCAL_PROXY_HTTP_PORT="+httpPort,
		"PULSE_LOCAL_PROXY_HTTPS_PORT="+httpsPort,
		"PULSE_LOCAL_PROXY_SKIP_TRUST_INSTALL=1",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start built app: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")
	client := insecureHTTPSClient()
	getJSONWithClient(t, client, "https://api.onlv.localhost:"+httpsPort+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
}

func TestPulseRunDashboardNotificationsAndMCP(t *testing.T) {
	repo := repoRoot(t)
	sourceAppDir := filepath.Join(repo, "testdata", "apps", "basic")
	appDir := filepath.Join(t.TempDir(), "basic")
	copyDir(t, sourceAppDir, appDir)
	rewriteFixtureReplace(t, filepath.Join(appDir, "go.mod"), repo)

	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	binary := buildPulseBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = pulseRunEnv(dashAddr)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pulse run: %v", err)
	}
	defer stopPulseProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")
	waitForHTTP(t, "http://"+dashAddr+"/basicapp")

	wsConn, _, err := websocket.DefaultDialer.Dial("ws://"+dashAddr+"/__pulse", nil)
	if err != nil {
		t.Fatalf("dial dashboard websocket: %v", err)
	}
	defer wsConn.Close()

	version := wsCall(t, wsConn, 1, "version", map[string]any{})
	if toString(version["version"]) == "" {
		t.Fatalf("dashboard version response missing version: %#v", version)
	}

	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
	waitForWSMethods(t, wsConn, 10*time.Second, "trace/new")

	mcp := openMCPClient(t, dashAddr, "basicapp")
	defer mcp.Close()

	initResp := mcp.Call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]any{
			"name":    "pulse-test",
			"version": "0.0.0",
		},
		"capabilities": map[string]any{},
	})
	if toString(toMap(initResp["serverInfo"])["name"]) != "pulse-mcp" {
		t.Fatalf("unexpected mcp initialize response: %#v", initResp)
	}

	toolsResp := mcp.Call(t, 2, "tools/list", map[string]any{})
	toolNames := mcpToolNames(toSlice(toolsResp["tools"]))
	for _, want := range []string{"get_services", "get_traces", "call_endpoint"} {
		if !toolNames[want] {
			t.Fatalf("mcp tools missing %q: %#v", want, toolsResp)
		}
	}

	servicesResp := mcp.CallTool(t, 3, "get_services", map[string]any{})
	services := servicesResp["structuredContent"]
	if !strings.Contains(fmt.Sprint(services), "service") {
		t.Fatalf("unexpected get_services response: %#v", servicesResp)
	}

	tracesResp := waitForMCPToolResult(t, 10*time.Second, func() map[string]any {
		return mcp.CallTool(t, 4, "get_traces", map[string]any{"limit": 10})
	})
	if !strings.Contains(fmt.Sprint(tracesResp["structuredContent"]), "trace_id") {
		t.Fatalf("unexpected get_traces response: %#v", tracesResp)
	}

	apiPath := filepath.Join(appDir, "service", "api.go")
	data, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), `Prefix: "hi"`, `Prefix: "dashboard"`, 1)
	if updated == string(data) {
		t.Fatal("failed to update test fixture source")
	}
	if err := os.WriteFile(apiPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	waitForWSMethods(t, wsConn, 15*time.Second, "process/compile-start", "process/output", "process/reload")
	waitForJSONResponse(t, "http://"+addr+"/service.CallPrivate", http.StatusOK, map[string]any{"message": "secret:dashboard"})
}

func buildPulseBinary(t *testing.T, repo string) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "pulse")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/pulse")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build pulse binary: %v\n%s", err, output)
	}
	return binPath
}

func pulseRunEnv(dashboardAddr string) []string {
	return append(
		os.Environ(),
		"PULSE_DEV_DASHBOARD_ADDR="+dashboardAddr,
		"PULSE_LOCAL_PROXY=0",
	)
}

func pulseRunProxyEnv(dashboardAddr, httpPort, httpsPort, frontendAddr string) []string {
	env := append(
		os.Environ(),
		"PULSE_DEV_DASHBOARD_ADDR="+dashboardAddr,
		"PULSE_LOCAL_PROXY_HTTP_PORT="+httpPort,
		"PULSE_LOCAL_PROXY_HTTPS_PORT="+httpsPort,
		"PULSE_LOCAL_PROXY_SKIP_TRUST_INSTALL=1",
	)
	if frontendAddr != "" {
		env = append(env, "PULSE_FRONTEND_ADDR="+frontendAddr)
	}
	return env
}

func stopPulseProcess(t *testing.T, cancel context.CancelFunc, cmd *exec.Cmd) {
	t.Helper()
	cancel()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for pulse process to exit")
	}
}

type mcpClient struct {
	t        *testing.T
	baseURL  string
	endpoint string
	reader   *bufio.Reader
	body     io.Closer
}

func openMCPClient(t *testing.T, dashAddr, appID string) *mcpClient {
	t.Helper()
	resp, err := http.Get("http://" + dashAddr + "/sse?app=" + url.QueryEscape(appID))
	if err != nil {
		t.Fatalf("open mcp sse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("unexpected mcp sse status %d: %s", resp.StatusCode, body)
	}
	reader := bufio.NewReader(resp.Body)
	event := readSSEEvent(t, reader, 10*time.Second)
	if event.event != "endpoint" {
		resp.Body.Close()
		t.Fatalf("unexpected first mcp event: %#v", event)
	}
	return &mcpClient{
		t:        t,
		baseURL:  "http://" + dashAddr,
		endpoint: strings.TrimSpace(event.data),
		reader:   reader,
		body:     resp.Body,
	}
}

func (c *mcpClient) Close() {
	if c != nil && c.body != nil {
		_ = c.body.Close()
	}
}

func (c *mcpClient) Call(t *testing.T, id int, method string, params map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(c.baseURL+c.endpoint, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("mcp post %s: %v", method, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected mcp post status %d for %s", resp.StatusCode, method)
	}
	for {
		event := readSSEEvent(t, c.reader, 10*time.Second)
		if event.event != "message" {
			continue
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(event.data), &response); err != nil {
			t.Fatalf("decode mcp response: %v", err)
		}
		if int(toFloat(response["id"])) != id {
			continue
		}
		if errPayload, ok := response["error"]; ok && errPayload != nil {
			t.Fatalf("mcp %s returned error: %#v", method, response)
		}
		return toMap(response["result"])
	}
}

func (c *mcpClient) CallTool(t *testing.T, id int, name string, args map[string]any) map[string]any {
	t.Helper()
	return c.Call(t, id, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

type sseEvent struct {
	event string
	data  string
}

func readSSEEvent(t *testing.T, reader *bufio.Reader, timeout time.Duration) sseEvent {
	t.Helper()
	type result struct {
		event sseEvent
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var event sseEvent
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if event.event != "" || event.data != "" {
					ch <- result{event: event}
					return
				}
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			if strings.HasPrefix(line, "event:") {
				event.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if event.data != "" {
					event.data += "\n"
				}
				event.data += part
			}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read sse event: %v", res.err)
		}
		return res.event
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for sse event")
		return sseEvent{}
	}
}

func wsCall(t *testing.T, conn *websocket.Conn, id int, method string, params map[string]any) map[string]any {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		t.Fatalf("write websocket rpc: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set websocket deadline: %v", err)
	}
	for time.Now().Before(deadline) {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			t.Fatalf("read websocket rpc: %v", err)
		}
		if int(toFloat(message["id"])) != id {
			continue
		}
		if errPayload, ok := message["error"]; ok && errPayload != nil {
			t.Fatalf("websocket rpc %s returned error: %#v", method, message)
		}
		return toMap(message["result"])
	}
	t.Fatalf("timed out waiting for websocket rpc response %s", method)
	return nil
}

func waitForWSMethods(t *testing.T, conn *websocket.Conn, timeout time.Duration, methods ...string) {
	t.Helper()
	remaining := make(map[string]bool, len(methods))
	for _, method := range methods {
		remaining[method] = true
	}
	deadline := time.Now().Add(timeout)
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set websocket deadline: %v", err)
	}
	for len(remaining) > 0 && time.Now().Before(deadline) {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			t.Fatalf("read websocket notification: %v", err)
		}
		method, _ := message["method"].(string)
		delete(remaining, method)
	}
	if len(remaining) > 0 {
		t.Fatalf("timed out waiting for websocket notifications: %v", remaining)
	}
}

func waitForMCPToolResult(t *testing.T, timeout time.Duration, fn func() map[string]any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result := fn()
		if strings.Contains(fmt.Sprint(result["structuredContent"]), "trace_id") {
			return result
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for mcp tool result")
	return nil
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	waitForURL(t, client, url)
}

func waitForURL(t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server did not start: %s", url)
}

func waitForJSONResponse(t *testing.T, url string, wantStatus int, want map[string]any) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var got map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if decodeErr == nil && resp.StatusCode == wantStatus && mapsEqual(got, want) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("response did not settle to %v at %s", want, url)
}

func waitForCronStatus(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var got map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if decodeErr == nil && resp.StatusCode == http.StatusOK &&
			toString(got["count"]) != "0" &&
			strings.TrimSpace(toString(got["cron"])) != "" &&
			toString(got["type"]) == "api-call" &&
			toString(got["path"]) == "/service.Run" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("cron job did not execute at %s", url)
}

func postJSON(t *testing.T, url string, body any, headers map[string]string, wantStatus int, want map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	assertJSONResponse(t, req, wantStatus, want)
}

func getJSON(t *testing.T, url string, headers map[string]string, wantStatus int, want map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	assertJSONResponse(t, req, wantStatus, want)
}

func getJSONWithClient(t *testing.T, client *http.Client, url string, headers map[string]string, wantStatus int, want map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	assertJSONResponseWithClient(t, client, req, wantStatus, want, nil)
}

func assertCORSPreflight(t *testing.T, url string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodOptions, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:5178")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected preflight status %d: %s", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get("Access-Control-Allow-Origin"), "http://localhost:5178"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want it to include GET", got)
	}
	if got := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers")); !strings.Contains(got, "authorization") || !strings.Contains(got, "content-type") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want authorization and content-type", got)
	}
	vary := resp.Header.Get("Vary")
	for _, want := range []string{"Origin", "Authorization", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if !strings.Contains(vary, want) {
			t.Fatalf("Vary = %q, want %q", vary, want)
		}
	}
}

func assertCORSActual(t *testing.T, url string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:5178")
	req.Header.Set("Authorization", "Bearer token123")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected actual CORS status %d: %s", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get("Access-Control-Allow-Origin"), "http://localhost:5178"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	vary := resp.Header.Get("Vary")
	for _, want := range []string{"Origin", "Authorization"} {
		if !strings.Contains(vary, want) {
			t.Fatalf("Vary = %q, want %q", vary, want)
		}
	}
}

func assertJSONResponse(t *testing.T, req *http.Request, wantStatus int, want map[string]any) {
	t.Helper()
	assertJSONResponseWithHeaders(t, req, wantStatus, want, nil)
}

func assertJSONResponseWithHeaders(t *testing.T, req *http.Request, wantStatus int, want map[string]any, wantHeaders map[string]string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	assertJSONResponseWithClient(t, client, req, wantStatus, want, wantHeaders)
}

func assertJSONResponseWithClient(t *testing.T, client *http.Client, req *http.Request, wantStatus int, want map[string]any, wantHeaders map[string]string) {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d, want %d: %s", resp.StatusCode, wantStatus, body)
	}
	for key, wantValue := range wantHeaders {
		if got := resp.Header.Get(key); got != wantValue {
			t.Fatalf("unexpected header %s=%q, want %q", key, got, wantValue)
		}
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(got, want) {
		t.Fatalf("unexpected body: got=%v want=%v", got, want)
	}
}

func insecureHTTPSClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func mustRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func mapsEqual(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			return false
		}
		if strings.TrimSpace(toString(gotValue)) != strings.TrimSpace(toString(wantValue)) {
			return false
		}
	}
	return true
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func toMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func toSlice(value any) []any {
	if value == nil {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func toFloat(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}

func mcpToolNames(items []any) map[string]bool {
	names := make(map[string]bool, len(items))
	for _, item := range items {
		name := toString(toMap(item)["name"])
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strings.Split(ln.Addr().String(), ":")[1]
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(wd)
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	}); err != nil {
		t.Fatal(err)
	}
}

func rewriteFixtureReplace(t *testing.T, goModPath, repo string) {
	t.Helper()
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "replace pulse.dev => ../../..", "replace pulse.dev => "+repo, 1)
	if updated == string(data) {
		t.Fatalf("expected fixture go.mod replace in %s", goModPath)
	}
	if err := os.WriteFile(goModPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePulseApp(t *testing.T, appDir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(appDir, "pulse.app"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
