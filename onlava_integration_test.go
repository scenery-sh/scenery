package onlava_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var (
	buildOnlavaBinaryOnce sync.Once
	buildOnlavaBinaryPath string
	buildOnlavaBinaryErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	stopSharedTemporalDevServer()
	os.Exit(code)
}

func TestOnlavaRunBasicApp(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "basic")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = append(withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-basic-v1"), "ONLAVA_CORS_ALLOW_ORIGINS=http://localhost:5178")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")

	postJSON(t, "http://"+addr+"/echo/Alice?title=Dr", map[string]string{"body": "body"}, map[string]string{"X-Echo": "hdr"}, http.StatusOK, map[string]any{"message": "hi Alice Dr hdr body"})
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
	getJSON(t, "http://"+addr+"/service.CustomStatus", nil, http.StatusCreated, map[string]any{"message": "created"})
	getJSON(t, "http://"+addr+"/service.AuthEcho", map[string]string{"Authorization": "Bearer token123"}, http.StatusOK, map[string]any{"user": "user-1", "role": "admin"})
	getJSON(t, "http://"+addr+"/raw/alpha/beta", nil, http.StatusOK, map[string]any{"path": "alpha/beta", "method": "GET"})
	assertCORSPreflight(t, "http://"+addr+"/service.AuthEcho")
	assertCORSActual(t, "http://"+addr+"/service.AuthEcho")
}

func TestOnlavaRunStandardAuthDevBootstrap(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "standard-auth")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-standard-auth-v1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/users/dev-bootstrap")
	token := postJSONForString(t, "http://"+addr+"/users/dev-bootstrap", map[string]string{
		"user_id":   "user-123",
		"tenant_id": "00000000-0000-0000-0000-000000000123",
	}, "token")
	getJSON(t, "http://"+addr+"/whoami", map[string]string{"Authorization": "Bearer " + token}, http.StatusOK, map[string]any{
		"user_id":   "user-123",
		"tenant_id": "00000000-0000-0000-0000-000000000123",
	})
}

func TestOnlavaRunLoadsSecretsFromDotEnv(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "secrets")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-secrets-v1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/secrets")
	getJSON(t, "http://"+addr+"/secrets", nil, http.StatusOK, map[string]any{
		"service": "service-secret",
		"helper":  "helper-secret",
	})
}

func TestOnlavaRunProductionFailsForMissingSecrets(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureAppVariant(t, repo, "secrets", "missing-env", nil, []string{".env"})
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr, "--env", "production")
	cmd.Env = withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-secrets-v1")
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("onlava run --env production succeeded with missing secrets; output:\n%s", output)
	}
	if ctx.Err() != nil {
		t.Fatalf("onlava run --env production timed out; output:\n%s", output)
	}
	got := string(output)
	for _, want := range []string{"missing required secrets for production"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not contain %q", got, want)
		}
	}
	if !strings.Contains(got, "ServiceSecret") && !strings.Contains(got, "HelperSecret") {
		t.Fatalf("output %q does not name a missing declared secret", got)
	}
}

func TestOnlavaRunPopulatesSecretsBeforeTemporalPackageDeclarations(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedSyntheticApp(t, "temporalsecrets", map[string]string{
		"go.mod":       "module example.com/temporalsecrets\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repo + "\n",
		".onlava.json": `{"name":"temporalsecrets"}`,
		".env":         "TestActivityTimeoutSeconds=10\n",
		"queue/api.go": `package queue

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/temporal"
)

var secrets struct {
	TestActivityTimeoutSeconds string
}

type Input struct {
	ID string ` + "`json:\"id\"`" + `
}

type Response struct {
	TimeoutSeconds int    ` + "`json:\"timeout_seconds\"`" + `
	Secret         string ` + "`json:\"secret\"`" + `
}

var activity = temporal.NewActivity[*Input, temporal.Void]("queue.Handle/v1", temporal.ActivityConfig{
	TaskQueue:    "queue.go",
	StartToClose: time.Duration(parsePositiveInt(secrets.TestActivityTimeoutSeconds, 1)) * time.Second,
}, func(context.Context, *Input) (temporal.Void, error) {
	return temporal.Void{}, nil
})

func parsePositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

//onlava:api public path=/concurrency method=GET
func Concurrency(ctx context.Context) (*Response, error) {
	return &Response{
		TimeoutSeconds: int(activity.Config().StartToClose / time.Second),
		Secret: secrets.TestActivityTimeoutSeconds,
	}, nil
}
`,
	})

	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)
	temporalAddr := startTemporalDevServerForTest(t, filepath.Join(t.TempDir(), "temporal-cache"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output strings.Builder
	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = append(withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-temporal-secrets-v1"), "TEMPORAL_ADDRESS="+temporalAddr)
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTPWithProcessOutput(t, "http://"+addr+"/concurrency", &output)
	getJSON(t, "http://"+addr+"/concurrency", nil, http.StatusOK, map[string]any{
		"timeout_seconds": 10,
		"secret":          "10",
	})
}

func TestOnlavaRunInitializesServiceStructsAtStartup(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedSyntheticApp(t, "serviceinit", map[string]string{
		"go.mod":       "module example.com/serviceinit\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repo + "\n",
		".onlava.json": `{"name":"serviceinit"}`,
		".env":         "# Fixture environment intentionally empty.\n",
		"svc/api.go": `package svc

import (
	"context"
	"os"
)

//onlava:service
type Service struct{}

func initService() (*Service, error) {
	if path := os.Getenv("ONLAVA_INIT_MARKER"); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0o644); err != nil {
			return nil, err
		}
	}
	return &Service{}, nil
}

//onlava:api public
func (s *Service) Hello(ctx context.Context) error { return nil }
`,
	})
	markerPath := filepath.Join(t.TempDir(), "init.marker")

	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = append(withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-service-init-v1"), "ONLAVA_INIT_MARKER="+markerPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForFile(t, markerPath)
}

func TestOnlavaRunMiddlewareApp(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "middleware")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = withSharedWorkspace(onlavaRunEnv(repo, dashAddr, cacheDir), "fixture-middleware-v1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

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

func TestOnlavaRunExecutesCronJobs(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "cron")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	tmpDir := t.TempDir()
	apiCacheDir := sharedIntegrationCache(t)
	workerCacheDir := apiCacheDir
	temporalCacheDir := filepath.Join(tmpDir, "temporal-cache")
	statePath := filepath.Join(tmpDir, "cron-state.json")
	binary := buildOnlavaBinary(t, repo)
	temporalAddr := startTemporalDevServerForTest(t, temporalCacheDir)
	commonEnv := []string{
		"TEMPORAL_ADDRESS=" + temporalAddr,
		"ONLAVA_CRON_STATE_PATH=" + statePath,
		"ONLAVA_TEST_TRIGGER_CRON_SCHEDULES=1",
		"ONLAVA_BUILD_ID=test",
	}
	apiEnv := append(withSharedWorkspace(onlavaRunEnv(repo, dashAddr, apiCacheDir), "fixture-cron-v1"), commonEnv...)
	workerEnv := append(withSharedWorkspace(onlavaRunEnv(repo, dashAddr, workerCacheDir), "fixture-cron-v1"), commonEnv...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	var workerOutput strings.Builder
	workerCmd := exec.CommandContext(workerCtx, binary, "worker")
	workerCmd.Env = workerEnv
	workerCmd.Stdout = &workerOutput
	workerCmd.Stderr = &workerOutput
	workerCmd.Stdin = nil
	workerCmd.Dir = appDir
	workerCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := workerCmd.Start(); err != nil {
		t.Fatalf("start onlava worker: %v", err)
	}
	defer killOnlavaProcess(t, workerCancel, workerCmd)

	var apiOutput strings.Builder
	cmd := exec.CommandContext(ctx, binary, "run", "--listen", addr)
	cmd.Env = apiEnv
	cmd.Stdout = &apiOutput
	cmd.Stderr = &apiOutput
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava run: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTPWithProcessOutput(t, "http://"+addr+"/cron/status", &apiOutput, &workerOutput)
	waitForCronStatus(t, "http://"+addr+"/cron/status")
}

func TestOnlavaBuiltBinaryIsHeadlessByDefault(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedSyntheticApp(t, "headless", map[string]string{
		"go.mod":       "module example.com/headless\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repo + "\n",
		".onlava.json": `{"name":"headlessapp","proxy":{"api_host":"api.acme.localhost","console_host":"console.acme.localhost","mcp_host":"mcp.acme.localhost","frontends":{"web":{"host":"web.acme.localhost"}}}}`,
		"svc/api.go": `package svc

import "context"

type Response struct {
	Message string ` + "`json:\"message\"`" + `
}

//onlava:api public
func Ping(context.Context) (*Response, error) {
	return &Response{Message: "pong"}, nil
}
`,
	})
	onlavaBinary := buildOnlavaBinary(t, repo)
	cacheDir := sharedIntegrationCache(t)
	outputPath := filepath.Join(cacheDir, "bin", "headless-app")

	buildCmd := exec.Command(onlavaBinary, "build", "-o", outputPath)
	buildCmd.Dir = appDir
	buildCmd.Env = withSharedWorkspace(append(os.Environ(), "GOMAXPROCS="+integrationChildGOMAXPROCS, "ONLAVA_DEV_CACHE_DIR="+cacheDir), "fixture-headless-build-v1")
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("onlava build failed: %v\n%s", err, buildOutput)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("built binary missing: %v", err)
	}

	port := freePort(t)
	addr := "127.0.0.1:" + port
	httpsPort := freePort(t)
	limitOnlavaProcessConcurrency(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, outputPath)
	cmd.Env = append(
		os.Environ(),
		"GOMAXPROCS="+integrationChildGOMAXPROCS,
		"ONLAVA_LISTEN_ADDR="+addr,
		"ONLAVA_LOCAL_PROXY_HTTPS_PORT="+httpsPort,
		"ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL=1",
		"ONLAVA_DEV_CACHE_DIR="+cacheDir,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start built app: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/svc.Ping")
	getJSON(t, "http://"+addr+"/svc.Ping", nil, http.StatusOK, map[string]any{"message": "pong"})
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+httpsPort, 50*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("built binary unexpectedly listened on local HTTPS proxy port %s", httpsPort)
	}
}

func TestOnlavaDevDashboardNotificationsAndMCP(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	serviceSource := `package service

import "context"

//onlava:service
type Service struct {
	Prefix string
}

func initService() (*Service, error) {
	return &Service{Prefix: "hi"}, nil
}

type Response struct {
	Message string ` + "`json:\"message\"`" + `
}

//onlava:api private
func (s *Service) Secret(ctx context.Context) (*Response, error) {
	return &Response{Message: "secret:" + s.Prefix}, nil
}

//onlava:api public
func (s *Service) CallPrivate(ctx context.Context) (*Response, error) {
	return s.Secret(ctx)
}
`
	appDir := cachedSyntheticApp(t, "devdashboard", map[string]string{
		"go.mod":         "module example.com/devdashboard\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repo + "\n",
		".onlava.json":   `{"name":"basicapp","proxy":{"workspace":"ignored","api_host":"api.acme.localhost","console_host":"console.acme.localhost","mcp_host":"mcp.acme.localhost","frontends":{"web":{"host":"web.acme.localhost"}}}}`,
		".env":           "# Fixture environment intentionally empty.\n",
		"service/api.go": serviceSource,
	})
	unlockFixture := lockIntegrationFixtureMutation(t, appDir)
	t.Cleanup(unlockFixture)
	apiPath := filepath.Join(appDir, "service", "api.go")
	writeFileIfChanged(t, apiPath, serviceSource)
	t.Cleanup(func() {
		writeFileIfChanged(t, apiPath, serviceSource)
	})

	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	httpPort := freePort(t)
	httpsPort := freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildOnlavaBinary(t, repo)

	frontendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	frontendAddr := frontendLn.Addr().String()
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

	cmd := exec.CommandContext(ctx, binary, "dev", "--listen", addr, "--proxy")
	cmd.Env = withSharedWorkspace(onlavaDevProxyEnv(repo, dashAddr, cacheDir, httpPort, httpsPort, frontendAddr), "fixture-basic-dev-v1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	limitOnlavaProcessConcurrency(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start onlava dev: %v", err)
	}
	defer killOnlavaProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")
	waitForHTTP(t, "http://"+dashAddr+"/basicapp")

	client := insecureHTTPSClient()
	apiURL := "https://api.acme.localhost:" + httpsPort + "/service.CallPrivate"
	getJSONWithClient(t, client, apiURL, nil, http.StatusOK, map[string]any{"message": "secret:hi"})

	consoleURL := "https://console.acme.localhost:" + httpsPort + "/"
	waitForURL(t, client, consoleURL)
	resp, err := client.Get(consoleURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("unexpected console status %d", resp.StatusCode)
	}

	mcpURL := "https://mcp.acme.localhost:" + httpsPort + "/sse?app=basicapp"
	waitForURL(t, client, mcpURL)
	resp, err = client.Get(mcpURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected mcp status %d", resp.StatusCode)
	}

	frontendURL := "https://web.acme.localhost:" + httpsPort + "/"
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

	wsConn, _, err := websocket.DefaultDialer.Dial("ws://"+dashAddr+"/__onlava", nil)
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
			"name":    "onlava-test",
			"version": "0.0.0",
		},
		"capabilities": map[string]any{},
	})
	if toString(toMap(initResp["serverInfo"])["name"]) != "onlava-mcp" {
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
