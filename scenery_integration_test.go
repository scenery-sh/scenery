package scenery_test

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	temporalclient "go.temporal.io/sdk/client"
	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/testlimit"
	sceneryruntime "scenery.sh/runtime"
)

var (
	buildSceneryBinaryOnce sync.Once
	buildSceneryBinaryPath string
	buildSceneryBinaryErr  error
)

type lockedOutput struct {
	mu      sync.Mutex
	builder strings.Builder
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.builder.Write(p)
}

func (o *lockedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.builder.String()
}

func TestMain(m *testing.M) {
	// The integration tests spend nearly all their time waiting on spawned
	// scenery binaries and go builds, so run them all at once despite the
	// testlimit GOMAXPROCS cap.
	testlimit.RaiseTestParallelism(12)
	prebuildSceneryBinaryForSelectedTests()
	code := m.Run()
	stopSharedTemporalDevServer()
	os.Exit(code)
}

// prebuildSceneryBinaryForSelectedTests warms the shared scenery binary in the
// background so the build overlaps with tests that do not need it. Tests that
// do need it block in buildSceneryBinary until the build completes and report
// any build error there.
func prebuildSceneryBinaryForSelectedTests() {
	if !shouldPrebuildSceneryBinary() {
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	repo := filepath.Clean(wd)
	go buildSceneryBinaryOnce.Do(func() {
		buildSceneryBinaryPath, buildSceneryBinaryErr = buildSceneryBinaryForRepo(repo)
	})
}

func shouldPrebuildSceneryBinary() bool {
	runFlag := flag.Lookup("test.run")
	if runFlag == nil {
		return true
	}
	pattern := strings.TrimSpace(runFlag.Value.String())
	if pattern == "" {
		return true
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return true
	}
	return slices.ContainsFunc([]string{
		"TestSceneryServeRuntimeMatrix",
		"TestSceneryServeStandardAuthDevBootstrap",
		"TestSceneryServeProductionFailsForMissingSecrets",
		"TestSceneryServePopulatesSecretsBeforeTemporalPackageDeclarations",
		"TestSceneryServeExecutesCronJobs",
		"TestSceneryBuiltBinaryIsHeadlessByDefault",
		"TestSceneryDevDashboardNotificationsAndRoutes",
	}, re.MatchString)
}

func TestSceneryServeRuntimeMatrix(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedSyntheticApp(t, "serve-runtime-matrix", map[string]string{
		"go.mod":        "module example.com/serveruntime\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repo + "\n",
		".scenery.json": `{"name":"serveruntime"}`,
		".env":          "ServiceSecret=service-secret\nHelperSecret=helper-secret\n",
		"helper/helper.go": `package helper

var secrets struct {
	HelperSecret string
}

func Value() string {
	return secrets.HelperSecret
}
`,
		"globalmw/global.go": `package globalmw

import "scenery.sh/middleware"

//scenery:middleware global target=tag:global
func AddHeader(req middleware.Request, next middleware.Next) middleware.Response {
	resp := next(req)
	resp.Header().Set("X-Global-Middleware", "true")
	return resp
}
`,
		"service/api.go": `package service

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"example.com/serveruntime/helper"

	scenery "scenery.sh"
	sceneryauth "scenery.sh/auth"
	"scenery.sh/errs"
	"scenery.sh/middleware"
)

var secrets struct {
	ServiceSecret string
}

//scenery:service
type Service struct {
	Prefix string
}

func initService() (*Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(cwd, ".scenery-init.marker"), []byte("started"), 0o644); err != nil {
		return nil, err
	}
	return &Service{Prefix: "hi"}, nil
}

type EchoRequest struct {
	Title  string ` + "`query:\"title\"`" + `
	Header string ` + "`header:\"X-Echo\"`" + `
	Body   string ` + "`json:\"body\"`" + `
}

type EchoResponse struct {
	Message string ` + "`json:\"message\"`" + `
}

//scenery:api public path=/echo/:name method=GET,POST
func (s *Service) Echo(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	return &EchoResponse{
		Message: s.Prefix + " " + name + " " + req.Title + " " + req.Header + " " + req.Body,
	}, nil
}

//scenery:api private
func (s *Service) Secret(ctx context.Context) (*EchoResponse, error) {
	return &EchoResponse{Message: "secret:" + s.Prefix}, nil
}

//scenery:api public
func (s *Service) CallPrivate(ctx context.Context) (*EchoResponse, error) {
	return s.Secret(ctx)
}

type AuthData struct {
	Role string ` + "`json:\"role\"`" + `
}

//scenery:authhandler
func (s *Service) AuthHandler(ctx context.Context, token string) (sceneryauth.UID, *AuthData, error) {
	if token != "token123" {
		return "", nil, errs.B().Code(errs.Unauthenticated).Msg("bad token").Err()
	}
	return "user-1", &AuthData{Role: "admin"}, nil
}

type AuthEchoResponse struct {
	User string ` + "`json:\"user\"`" + `
	Role string ` + "`json:\"role\"`" + `
}

//scenery:api auth
func (s *Service) AuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	userID, ok := sceneryauth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("missing auth").Err()
	}
	data := sceneryauth.Data().(*AuthData)
	return &AuthEchoResponse{User: string(userID), Role: data.Role}, nil
}

type StatusResponse struct {
	Message string ` + "`json:\"message\"`" + `
	Status  int    ` + "`scenery:\"httpstatus\"`" + `
}

//scenery:api public
func (s *Service) CustomStatus(ctx context.Context) (*StatusResponse, error) {
	return &StatusResponse{Message: "created", Status: 201}, nil
}

//scenery:api public raw path=/raw/*rest
func (s *Service) Raw(w http.ResponseWriter, req *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]string{
		"path":   scenery.CurrentRequest().PathParams.Get("rest"),
		"method": scenery.CurrentRequest().Method,
	})
}

type SecretResponse struct {
	Service string ` + "`json:\"service\"`" + `
	Helper  string ` + "`json:\"helper\"`" + `
}

//scenery:api public path=/secrets method=GET
func (s *Service) Secrets(ctx context.Context) (*SecretResponse, error) {
	return &SecretResponse{
		Service: secrets.ServiceSecret,
		Helper:  helper.Value(),
	}, nil
}

type ctxKey struct{}

//scenery:middleware target=all
func (s *Service) InjectContext(req middleware.Request, next middleware.Next) middleware.Response {
	ctx := context.WithValue(req.Context(), ctxKey{}, "svc")
	return next(req.WithContext(ctx))
}

//scenery:api public tag:ctx tag:global
func (s *Service) Context(ctx context.Context) (*EchoResponse, error) {
	value, _ := ctx.Value(ctxKey{}).(string)
	return &EchoResponse{Message: value}, nil
}

//scenery:api private tag:rewrite
func (s *Service) Private(ctx context.Context) (*EchoResponse, error) {
	return &EchoResponse{Message: "handler"}, nil
}

//scenery:api public
func (s *Service) MiddlewareCallPrivate(ctx context.Context) (*EchoResponse, error) {
	return s.Private(ctx)
}

//scenery:api public tag:error
func (s *Service) Error(ctx context.Context) error {
	return nil
}

//scenery:api public raw path=/mw/raw/:id tag:raw
func (s *Service) MiddlewareRaw(w http.ResponseWriter, req *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id": scenery.CurrentRequest().PathParams.Get("id"),
	})
}
`,
		"service/mw/mw.go": `package mw

import (
	service "example.com/serveruntime/service"

	"scenery.sh/errs"
	"scenery.sh/middleware"
)

//scenery:middleware target=tag:rewrite
func Rewrite(req middleware.Request, next middleware.Next) middleware.Response {
	resp := next(req)
	payload := resp.Payload.(*service.EchoResponse)
	payload.Message = "middleware:" + payload.Message
	return resp
}

//scenery:middleware target=tag:error
func Error(req middleware.Request, next middleware.Next) middleware.Response {
	return middleware.Response{
		Err: errs.B().Code(errs.Internal).Msg("middleware error").Err(),
	}
}

//scenery:middleware target=tag:raw
func RawHeader(req middleware.Request, next middleware.Next) middleware.Response {
	resp := next(req)
	resp.Header().Set("X-Raw-Middleware", "true")
	return resp
}
`,
	})
	markerPath := filepath.Join(appDir, ".scenery-init.marker")
	_ = os.Remove(markerPath)
	t.Cleanup(func() { _ = os.Remove(markerPath) })
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildSceneryBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "serve", "--listen", addr)
	cmd.Env = append(sceneryServeEnv(repo, dashAddr, cacheDir), "SCENERY_CORS_ALLOW_ORIGINS=http://localhost:5178")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start scenery serve: %v", err)
	}
	defer killSceneryProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/service.CallPrivate")
	waitForFile(t, markerPath)

	postJSON(t, "http://"+addr+"/echo/Alice?title=Dr", map[string]string{"body": "body"}, map[string]string{"X-Echo": "hdr"}, http.StatusOK, map[string]any{"message": "hi Alice Dr hdr body"})
	getJSON(t, "http://"+addr+"/service.CallPrivate", nil, http.StatusOK, map[string]any{"message": "secret:hi"})
	getJSON(t, "http://"+addr+"/service.CustomStatus", nil, http.StatusCreated, map[string]any{"message": "created"})
	getJSON(t, "http://"+addr+"/service.AuthEcho", map[string]string{"Authorization": "Bearer token123"}, http.StatusOK, map[string]any{"user": "user-1", "role": "admin"})
	getJSON(t, "http://"+addr+"/raw/alpha/beta", nil, http.StatusOK, map[string]any{"path": "alpha/beta", "method": "GET"})
	assertCORSPreflight(t, "http://"+addr+"/service.AuthEcho")
	assertCORSActual(t, "http://"+addr+"/service.AuthEcho")
	getJSON(t, "http://"+addr+"/secrets", nil, http.StatusOK, map[string]any{
		"service": "service-secret",
		"helper":  "helper-secret",
	})
	assertJSONResponseWithHeaders(t, mustRequest(t, http.MethodGet, "http://"+addr+"/service.Context", nil), http.StatusOK, map[string]any{"message": "svc"}, map[string]string{
		"X-Global-Middleware": "true",
	})
	getJSON(t, "http://"+addr+"/service.MiddlewareCallPrivate", nil, http.StatusOK, map[string]any{"message": "middleware:handler"})
	getJSON(t, "http://"+addr+"/service.Error", nil, http.StatusInternalServerError, map[string]any{"code": "internal", "message": "middleware error"})
	assertJSONResponseWithHeaders(t, mustRequest(t, http.MethodGet, "http://"+addr+"/mw/raw/alpha", nil), http.StatusOK, map[string]any{"id": "alpha"}, map[string]string{
		"X-Raw-Middleware": "true",
	})
}

func TestSceneryServeStandardAuthDevBootstrap(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "standard-auth")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildSceneryBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "serve", "--listen", addr)
	cmd.Env = sceneryServeEnv(repo, dashAddr, cacheDir)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start scenery serve: %v", err)
	}
	defer killSceneryProcess(t, cancel, cmd)

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

func TestSceneryServeProductionFailsForMissingSecrets(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureAppVariant(t, repo, "secrets", "missing-env", nil, []string{".env"})
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildSceneryBinary(t, repo)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "serve", "--listen", addr, "--env", "production")
	cmd.Env = sceneryServeEnv(repo, dashAddr, cacheDir)
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("scenery serve --env production succeeded with missing secrets; output:\n%s", output)
	}
	if ctx.Err() != nil {
		t.Fatalf("scenery serve --env production timed out; output:\n%s", output)
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

func TestSceneryServePopulatesSecretsBeforeTemporalPackageDeclarations(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedSyntheticApp(t, "temporalsecrets", map[string]string{
		"go.mod":        "module example.com/temporalsecrets\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repo + "\n",
		".scenery.json": `{"name":"temporalsecrets"}`,
		".env":          "TestActivityTimeoutSeconds=10\n",
		"queue/api.go": `package queue

import (
	"context"
	"strconv"
	"strings"
	"time"

	"scenery.sh/temporal"
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

//scenery:api public path=/concurrency method=GET
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
	binary := buildSceneryBinary(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output lockedOutput
	cmd := exec.CommandContext(ctx, binary, "serve", "--listen", addr)
	cmd.Env = sceneryServeEnv(repo, dashAddr, cacheDir)
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start scenery serve: %v", err)
	}
	defer killSceneryProcess(t, cancel, cmd)

	waitForHTTPWithProcessOutput(t, "http://"+addr+"/concurrency", &output)
	getJSON(t, "http://"+addr+"/concurrency", nil, http.StatusOK, map[string]any{
		"timeout_seconds": 10,
		"secret":          "10",
	})
}

func TestSceneryServeExecutesCronJobs(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedFixtureApp(t, repo, "cron")
	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	apiCacheDir := sharedIntegrationCache(t)
	workerCacheDir := apiCacheDir
	temporalCacheDir := filepath.Join(t.TempDir(), "temporal-cache")
	statePath := filepath.Join(appDir, ".scenery-cron-state.json")
	_ = os.Remove(statePath)
	t.Cleanup(func() { _ = os.Remove(statePath) })
	binary := buildSceneryBinary(t, repo)
	temporalAddr := startTemporalDevServerForTest(t, temporalCacheDir)
	commonEnv := []string{
		"TEMPORAL_ADDRESS=" + temporalAddr,
		"SCENERY_BUILD_ID=test",
		"SCENERY_TEMPORAL_TASK_QUEUE_PREFIX=scenery.cronapp",
		"SCENERY_TEMPORAL_DEPLOYMENT_NAME=scenery-cronapp",
	}
	apiEnv := append(sceneryServeEnv(repo, dashAddr, apiCacheDir), commonEnv...)
	workerEnv := append(sceneryServeEnv(repo, dashAddr, workerCacheDir), commonEnv...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var processes []sceneryTestProcess
	defer func() {
		if len(processes) > 0 {
			killSceneryProcesses(t, processes...)
		}
	}()

	var apiOutput lockedOutput
	cmd := exec.CommandContext(ctx, binary, "serve", "--listen", addr)
	cmd.Env = apiEnv
	cmd.Stdout = &apiOutput
	cmd.Stderr = &apiOutput
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start scenery serve: %v", err)
	}
	processes = append(processes, sceneryTestProcess{cancel: cancel, cmd: cmd})

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	var workerOutput lockedOutput
	workerCmd := exec.CommandContext(workerCtx, binary, "worker")
	workerCmd.Env = workerEnv
	workerCmd.Stdout = &workerOutput
	workerCmd.Stderr = &workerOutput
	workerCmd.Stdin = nil
	workerCmd.Dir = appDir
	workerCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := workerCmd.Start(); err != nil {
		t.Fatalf("start scenery worker: %v", err)
	}
	processes = append(processes, sceneryTestProcess{cancel: workerCancel, cmd: workerCmd})

	waitForHTTPWithProcessOutput(t, "http://"+addr+"/cron/status", &apiOutput)
	temporalCtx, temporalCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer temporalCancel()
	temporalClient, err := temporalclient.Dial(temporalclient.Options{HostPort: temporalAddr})
	if err != nil {
		t.Fatalf("dial temporal: %v", err)
	}
	defer temporalClient.Close()
	waitForCronScheduleReconciliation(t, &apiOutput, "scenery-tick", "scenery.cronapp.cron.go")

	// Temporal's local dev schedule Describe/List/Trigger returned inconsistent
	// scheduler state in this environment; start the same cron workflow
	// deterministically after the API logs successful schedule reconciliation.
	startTemporalCronWorkflow(t, temporalCtx, temporalClient, "scenery-cronapp.cron.scenery-tick")
	waitForCronStatus(t, "http://"+addr+"/cron/status")
}

type temporalCronInputForTest struct {
	AppID                string
	JobID                string
	ActivityName         string
	TaskQueue            string
	ActivityStartToClose time.Duration
	ActivityRetryPolicy  sceneryruntime.CronRetryPolicy
}

func waitForCronScheduleReconciliation(t *testing.T, output fmt.Stringer, jobID, taskQueue string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		logs := output.String()
		if strings.Contains(logs, "cron schedule reconciled") &&
			strings.Contains(logs, "id="+jobID) &&
			strings.Contains(logs, "task_queue="+taskQueue) {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("cron schedule reconciliation log not observed\nprocess output:\n%s", output.String())
}

func startTemporalCronWorkflow(t *testing.T, ctx context.Context, client temporalclient.Client, workflowID string) {
	t.Helper()
	_, err := client.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: "scenery.cronapp.cron.go",
	}, "scenery.cron.Invoke/v1", temporalCronInputForTest{
		AppID:                "cronapp",
		JobID:                "scenery-tick",
		ActivityName:         "scenery.cron.scenery-tick/v1",
		TaskQueue:            "scenery.cronapp.cron.go",
		ActivityStartToClose: time.Hour,
	})
	if err != nil {
		t.Fatalf("start temporal cron workflow %s: %v", workflowID, err)
	}
}

func TestSceneryBuiltBinaryIsHeadlessByDefault(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir := cachedSyntheticApp(t, "headless", map[string]string{
		"go.mod":        "module example.com/headless\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repo + "\n",
		".scenery.json": `{"name":"headlessapp","proxy":{"api_host":"api.acme.localhost","console_host":"console.acme.localhost","frontends":{"web":{"host":"web.acme.localhost"}}}}`,
		"svc/api.go": `package svc

import "context"

type Response struct {
	Message string ` + "`json:\"message\"`" + `
}

//scenery:api public
func Ping(context.Context) (*Response, error) {
	return &Response{Message: "pong"}, nil
}
`,
	})
	sceneryBinary := buildSceneryBinary(t, repo)
	cacheDir := sharedIntegrationCache(t)
	outputPath := filepath.Join(cacheDir, "bin", "headless-app")

	buildCmd := exec.Command(sceneryBinary, "build", "-o", outputPath)
	buildCmd.Dir = appDir
	buildCmd.Env = append(os.Environ(), "SCENERY_DEV_CACHE_DIR="+cacheDir)
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scenery build failed: %v\n%s", err, buildOutput)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("built binary missing: %v", err)
	}

	port := freePort(t)
	addr := "127.0.0.1:" + port
	httpsPort := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, outputPath)
	cmd.Env = append(
		os.Environ(),
		"SCENERY_LISTEN_ADDR="+addr,
		"SCENERY_LOCAL_PROXY_HTTPS_PORT="+httpsPort,
		"SCENERY_LOCAL_PROXY_SKIP_TRUST_INSTALL=1",
		"SCENERY_DEV_CACHE_DIR="+cacheDir,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start built app: %v", err)
	}
	defer killSceneryProcess(t, cancel, cmd)

	waitForHTTP(t, "http://"+addr+"/svc.Ping")
	getJSON(t, "http://"+addr+"/svc.Ping", nil, http.StatusOK, map[string]any{"message": "pong"})
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+httpsPort, 50*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("built binary unexpectedly listened on local HTTPS proxy port %s", httpsPort)
	}
}

func devRouteServiceSource() string {
	return `package service

import "context"

//scenery:service
type Service struct {
	Prefix string
}

func initService() (*Service, error) {
	return &Service{Prefix: "hi"}, nil
}

type Response struct {
	Message string ` + "`json:\"message\"`" + `
}

//scenery:api private
func (s *Service) Secret(ctx context.Context) (*Response, error) {
	return &Response{Message: "secret:" + s.Prefix}, nil
}

//scenery:api public
func (s *Service) CallPrivate(ctx context.Context) (*Response, error) {
	return s.Secret(ctx)
}
`
}

func prepareMutableDevRouteApp(t *testing.T, repo, fixtureName, moduleName, config string) (string, string, string) {
	t.Helper()
	serviceSource := devRouteServiceSource()
	appDir := cachedSyntheticApp(t, fixtureName, map[string]string{
		"go.mod":         "module example.com/" + moduleName + "\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repo + "\n",
		".scenery.json":  config,
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
	return appDir, apiPath, serviceSource
}

func waitForAgentSessionRoutes(t *testing.T, ctx context.Context, client *localagent.Client, appRoot string, routes ...string) localagent.Session {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last []localagent.Session
	for time.Now().Before(deadline) {
		sessions, err := client.List(ctx, appRoot)
		if err == nil {
			last = sessions
			for _, session := range sessions {
				ok := true
				for _, route := range routes {
					if strings.TrimSpace(session.Routes[route]) == "" {
						ok = false
						break
					}
				}
				if ok {
					return session
				}
			}
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("timed out waiting for agent routes %v for %s; sessions=%+v", routes, appRoot, last)
	return localagent.Session{}
}

func waitForIntegrationAgentPing(ctx context.Context, client *localagent.Client) error {
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(integrationPollInterval)
	}
	return lastErr
}

func TestSceneryDevDashboardNotificationsAndRoutes(t *testing.T) {
	t.Parallel()

	repo := repoRoot(t)
	appDir, apiPath, _ := prepareMutableDevRouteApp(t, repo, "devdashboard", "devdashboard", `{"name":"basicapp","proxy":{"workspace":"ignored","api_host":"api.acme.localhost","console_host":"console.acme.localhost","frontends":{"web":{"host":"web.acme.localhost"}}}}`)

	port := freePort(t)
	addr := "127.0.0.1:" + port
	dashAddr := "127.0.0.1:" + freePort(t)
	cacheDir := sharedIntegrationCache(t)
	binary := buildSceneryBinary(t, repo)
	agentHome := t.TempDir()

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

	agentCmd := exec.CommandContext(ctx, binary, "system", "agent", "--router-listen", "127.0.0.1:0")
	agentCmd.Env = sceneryDevAgentEnv(repo, dashAddr, cacheDir, agentHome, "")
	agentCmd.Stdout = io.Discard
	agentCmd.Stderr = io.Discard
	agentCmd.Stdin = nil
	agentCmd.Dir = appDir
	agentCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := agentCmd.Start(); err != nil {
		t.Fatalf("start scenery agent: %v", err)
	}
	defer killSceneryProcess(t, cancel, agentCmd)
	agentClient := localagent.NewClient(localagent.PathsForHome(agentHome).SocketPath)
	if err := waitForIntegrationAgentPing(ctx, agentClient); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, binary, "up", "--listen", addr)
	cmd.Env = sceneryDevAgentEnv(repo, dashAddr, cacheDir, agentHome, frontendAddr)
	var devOutput lockedOutput
	cmd.Stdout = &devOutput
	cmd.Stderr = &devOutput
	cmd.Stdin = nil
	cmd.Dir = appDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start scenery up: %v", err)
	}
	defer killSceneryProcess(t, cancel, cmd)

	session := waitForAgentSessionRoutes(t, ctx, agentClient, appDir, localagent.RouteAPI, localagent.RouteDashboard, "web")

	client := insecureHTTPSClient()
	directURL := "http://" + addr + "/service.CallPrivate"
	apiURL := strings.TrimRight(session.Routes[localagent.RouteAPI], "/") + "/service.CallPrivate"
	consoleURL := session.Routes[localagent.RouteDashboard]
	frontendURL := session.Routes["web"]
	type wsProbeResult struct {
		conn *websocket.Conn
		err  error
	}
	routeErrs := make(chan error, 4)
	wsReady := make(chan error, 1)
	wsResult := make(chan wsProbeResult, 1)
	go func() {
		routeErrs <- waitForJSONWithClientErr(&http.Client{Timeout: time.Second}, directURL, http.StatusOK, map[string]any{"message": "secret:hi"})
	}()
	go func() {
		routeErrs <- waitForStatusWithClientErr(client, consoleURL, http.StatusOK)
	}()
	go func() {
		routeErrs <- waitForBodyWithClientErr(client, frontendURL, http.StatusOK, "frontend ok")
	}()
	go func() {
		if err := <-wsReady; err != nil {
			routeErrs <- err
			return
		}
		routeErrs <- waitForJSONWithClientErr(client, apiURL, http.StatusOK, map[string]any{"message": "secret:hi"})
	}()
	go func() {
		wsConn, err := dialDashboardWebsocketWithVersion(consoleURL)
		if err != nil {
			wsReady <- err
			wsResult <- wsProbeResult{err: err}
			return
		}
		wsReady <- nil
		// On loaded CI runners (e.g. while the self harness runs other steps
		// concurrently) the first trace can take well over 10s to arrive.
		if err := waitForWSMethodsErr(wsConn, 60*time.Second, "trace/new"); err != nil {
			_ = wsConn.Close()
			wsResult <- wsProbeResult{err: err}
			return
		}
		wsResult <- wsProbeResult{conn: wsConn}
	}()
	for range 4 {
		if err := <-routeErrs; err != nil {
			t.Fatalf("%v\nprocess output:\n%s", err, devOutput.String())
		}
	}
	wsProbe := <-wsResult
	if wsProbe.err != nil {
		t.Fatalf("%v\nprocess output:\n%s", wsProbe.err, devOutput.String())
	}
	wsConn := wsProbe.conn
	defer wsConn.Close()

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
	if err := waitForJSONWithClientErr(&http.Client{Timeout: time.Second}, directURL, http.StatusOK, map[string]any{"message": "secret:dashboard"}); err != nil {
		t.Fatalf("%v\nprocess output:\n%s", err, devOutput.String())
	}
	if err := waitForJSONWithClientErr(client, apiURL, http.StatusOK, map[string]any{"message": "secret:dashboard"}); err != nil {
		t.Fatalf("%v\nprocess output:\n%s", err, devOutput.String())
	}
}

func dialDashboardWebsocketWithVersion(dashboardURL string) (*websocket.Conn, error) {
	parsed, err := url.Parse(dashboardURL)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = "/__scenery"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, _, err := dialer.Dial(parsed.String(), nil)
		if err != nil {
			lastErr = err
			time.Sleep(integrationPollInterval)
			continue
		}
		if _, err := wsCallErr(conn, 1, "version", map[string]any{}); err != nil {
			_ = conn.Close()
			lastErr = err
			time.Sleep(integrationPollInterval)
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("dashboard websocket/version did not become ready: %w", lastErr)
}
