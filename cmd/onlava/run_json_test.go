package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pbrazdil/onlava/internal/app"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

func silenceCLIStderr(t *testing.T) {
	t.Helper()
	old := cliStderr
	cliStderr = io.Discard
	t.Cleanup(func() { cliStderr = old })
}

func TestParseRunArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseRunArgs([]string{"--port", "4444", "--listen", "0.0.0.0", "--app-root", "/tmp/app", "--env", "production", "--log-format", "json"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if opts.Port != 4444 || opts.Listen != "0.0.0.0" || opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseRunArgsRejectsDevFlags(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--verbose", "--json", "--watch", "--dashboard", "--proxy"} {
		if _, err := parseRunArgs([]string{flag}); err == nil {
			t.Fatalf("parseRunArgs(%q) returned nil error", flag)
		}
	}
}

func TestParseDevArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDevArgs([]string{"--port", "4444", "--listen", "0.0.0.0", "--verbose", "--json", "--app-root", "/tmp/app", "--session", "review-a", "--proxy", "--trust", "--detach"})
	if err != nil {
		t.Fatalf("parseDevArgs returned error: %v", err)
	}
	if opts.Port != 4444 || opts.Listen != "0.0.0.0" || !opts.PortSet || !opts.ListenSet || !opts.Verbose || !opts.JSON || opts.AppRoot != "/tmp/app" || opts.SessionID != "review-a" || !opts.Proxy || !opts.Trust || !opts.Detach {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseDevArgsRejectsSessionAndNewSession(t *testing.T) {
	t.Parallel()

	if _, err := parseDevArgs([]string{"--session", "review-a", "--new-session"}); err == nil {
		t.Fatal("expected --session with --new-session to fail")
	}
}

func TestDevCommandUsesWatcherPath(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if listen.Network != "tcp" || listen.Addr != "127.0.0.1:4444" || !listen.Explicit || listen.SessionID != "review-a" || !verbose || !jsonMode || appRoot != "/tmp/app" {
			t.Fatalf("watch args = %+v %v %v %q", listen, verbose, jsonMode, appRoot)
		}
		if got := getenvForTest("ONLAVA_LOCAL_PROXY"); got != "1" {
			t.Fatalf("ONLAVA_LOCAL_PROXY = %q, want 1", got)
		}
		if got := getenvForTest("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); got != "0" {
			t.Fatalf("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL = %q, want 0", got)
		}
		return nil
	}

	if err := devCommand([]string{"--port", "4444", "--verbose", "--json", "--app-root", "/tmp/app", "--session", "review-a", "--proxy"}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandUsesDetachedPath(t *testing.T) {
	prevDetached := runDetachedDevFunc
	prevWatch := runWithWatchFunc
	defer func() {
		runDetachedDevFunc = prevDetached
		runWithWatchFunc = prevWatch
	}()

	detachedCalled := false
	runDetachedDevFunc = func(args []string, opts devOptions) error {
		detachedCalled = true
		if !reflect.DeepEqual(args, []string{"--app-root", "/tmp/app", "--detach"}) {
			t.Fatalf("detached args = %#v", args)
		}
		if !opts.Detach || opts.AppRoot != "/tmp/app" {
			t.Fatalf("detached opts = %+v", opts)
		}
		return nil
	}
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		t.Fatalf("watcher should not run for detached parent")
		return nil
	}

	if err := devCommand([]string{"--app-root", "/tmp/app", "--detach"}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !detachedCalled {
		t.Fatal("expected detached path to be called")
	}
}

func TestDevCommandDetachedChildUsesWatcherPath(t *testing.T) {
	prevDetached := runDetachedDevFunc
	prevWatch := runWithWatchFunc
	defer func() {
		runDetachedDevFunc = prevDetached
		runWithWatchFunc = prevWatch
	}()
	t.Setenv(detachedDevChildEnv, "1")

	watchCalled := false
	runDetachedDevFunc = func(args []string, opts devOptions) error {
		t.Fatalf("detached launcher should not run inside detached child")
		return nil
	}
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		watchCalled = true
		if appRoot != "/tmp/app" {
			t.Fatalf("appRoot = %q", appRoot)
		}
		return nil
	}

	if err := devCommand([]string{"--app-root", "/tmp/app", "--detach"}); err != nil {
		t.Fatalf("devCommand returned error: %v", err)
	}
	if !watchCalled {
		t.Fatal("expected watcher path to be called")
	}
}

func TestDevCommandDoesNotEnableProxyByDefault(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY", "")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if listen.Addr != "" || listen.Network != "" || listen.Explicit || listen.PreferTCP {
			t.Fatalf("listen = %+v, want agent/private default", listen)
		}
		if got := getenvForTest("ONLAVA_LOCAL_PROXY"); got != "" {
			t.Fatalf("ONLAVA_LOCAL_PROXY = %q, want empty", got)
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
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
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
	silenceCLIStderr(t)
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY", "0")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if !listen.PreferTCP {
			t.Fatalf("listen = %+v, want TCP preference for proxy", listen)
		}
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
	t.Setenv("ONLAVA_LOCAL_PROXY", "")
	t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", "1")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
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
	silenceCLIStderr(t)
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL", "1")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if !listen.PreferTCP {
			t.Fatalf("listen = %+v, want TCP preference for trust proxy", listen)
		}
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

func TestDevCommandProxyEnvPrefersTCP(t *testing.T) {
	silenceCLIStderr(t)
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("ONLAVA_LOCAL_PROXY", "1")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if !listen.PreferTCP {
			t.Fatalf("listen = %+v, want TCP preference for proxy env", listen)
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

func TestWarnDevEscapeHatchesProxyMode(t *testing.T) {
	var buf bytes.Buffer
	old := cliStderr
	cliStderr = &buf
	t.Cleanup(func() { cliStderr = old })

	warnDevEscapeHatches(devOptions{Proxy: true})

	if got := buf.String(); !strings.Contains(got, "legacy machine-global proxy ports") {
		t.Fatalf("warning = %q", got)
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

func TestHeadlessRuntimeRoleUsesAPI(t *testing.T) {
	t.Parallel()

	for _, cfg := range []app.Config{
		{},
		{Temporal: app.TemporalConfig{Enabled: true}},
	} {
		if got := headlessRuntimeRole(cfg); got != "api" {
			t.Fatalf("role = %q, want api", got)
		}
	}
}

func TestParseWorkerArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseWorkerArgs([]string{"--app-root", "/tmp/app", "--env", "production", "--log-format", "json", "--task-queue", "onlava.app.worker.go"})
	if err != nil {
		t.Fatalf("parseWorkerArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" || !reflect.DeepEqual(opts.TaskQueues, []string{"onlava.app.worker.go"}) {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseWorkerArgsSplitsRepeatedTaskQueues(t *testing.T) {
	t.Parallel()

	opts, err := parseWorkerArgs([]string{"--task-queue", "onlava.app.worker.go, onlava.app.email.go", "--task-queue", "onlava.app.worker.go"})
	if err != nil {
		t.Fatalf("parseWorkerArgs returned error: %v", err)
	}
	want := []string{"onlava.app.worker.go", "onlava.app.email.go", "onlava.app.worker.go"}
	if !reflect.DeepEqual(opts.TaskQueues, want) {
		t.Fatalf("TaskQueues = %#v, want %#v", opts.TaskQueues, want)
	}
	if got := uniqueWorkerTaskQueues(opts.TaskQueues); !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("uniqueWorkerTaskQueues = %#v, want %#v", got, want[:2])
	}
}

func TestParseWorkerArgsRejectsServerFlags(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--port", "--listen", "--verbose", "--json", "--watch", "--dashboard", "--proxy"} {
		if _, err := parseWorkerArgs([]string{flag}); err == nil {
			t.Fatalf("parseWorkerArgs(%q) returned nil error", flag)
		}
	}
}

func TestWorkerCommandUsesWorkerPath(t *testing.T) {
	prev := runWorkerFunc
	defer func() { runWorkerFunc = prev }()

	called := false
	runWorkerFunc = func(opts workerOptions) error {
		called = true
		if opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" || !reflect.DeepEqual(opts.TaskQueues, []string{"onlava.app.worker.go"}) {
			t.Fatalf("worker opts = %+v", opts)
		}
		return nil
	}

	if err := workerCommand([]string{"--app-root", "/tmp/app", "--env", "production", "--log-format", "json", "--task-queue", "onlava.app.worker.go"}); err != nil {
		t.Fatalf("workerCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected worker path to be called")
	}
}

func TestParseWorkerTypeScriptArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseWorkerTypeScriptArgs([]string{"--app-root", "/tmp/app", "--runtime", "bun", "--task-queue", "onlv.house.preview.ts,onlv.maps.earth.ts", "--generate-only"})
	if err != nil {
		t.Fatalf("parseWorkerTypeScriptArgs returned error: %v", err)
	}
	wantQueues := []string{"onlv.house.preview.ts", "onlv.maps.earth.ts"}
	if opts.AppRoot != "/tmp/app" || opts.Runtime != "bun" || !opts.GenerateOnly || !reflect.DeepEqual(opts.TaskQueues, wantQueues) {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseWorkerTypeScriptArgs([]string{"--runtime", "deno"}); err == nil {
		t.Fatal("expected invalid runtime error")
	}
}

func TestWorkerCommandUsesTypeScriptPath(t *testing.T) {
	prev := runWorkerTypeScriptFunc
	defer func() { runWorkerTypeScriptFunc = prev }()

	called := false
	runWorkerTypeScriptFunc = func(opts workerTypeScriptOptions, stdout io.Writer) error {
		called = true
		if opts.AppRoot != "/tmp/app" || opts.Runtime != "bun" || !reflect.DeepEqual(opts.TaskQueues, []string{"onlv.house.preview.ts"}) {
			t.Fatalf("typescript worker opts = %+v", opts)
		}
		return nil
	}

	if err := workerCommand([]string{"typescript", "--app-root", "/tmp/app", "--runtime", "bun", "--task-queue", "onlv.house.preview.ts"}); err != nil {
		t.Fatalf("workerCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected TypeScript worker path to be called")
	}
}

func TestParseWorkerBindingsArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseWorkerBindingsArgs([]string{"--app-root", "/tmp/app", "--out", "/tmp/out", "--json"})
	if err != nil {
		t.Fatalf("parseWorkerBindingsArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.OutDir != "/tmp/out" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestRunWorkerTypeScriptGenerateOnlyWritesRuntime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"orders"}`)
	writeTestAppFile(t, root, "house/preview.worker.ts", `import { activity } from "onlava/worker";
export type RenderInput = { id: string };
export type RenderOutput = { url: string };
export const render = activity<RenderInput, RenderOutput>({
  name: "house.Render/v1",
  taskQueue: "onlv.house.preview.ts"
}, async (_ctx, input) => ({ url: input.id }));
`)
	var out bytes.Buffer
	if err := runWorkerTypeScript(workerTypeScriptOptions{AppRoot: root, GenerateOnly: true, TaskQueues: []string{"onlv.house.preview.ts"}}, &out); err != nil {
		t.Fatalf("runWorkerTypeScript returned error: %v", err)
	}
	if !strings.Contains(out.String(), ".onlava/generated/temporal/typescript/worker.ts") {
		t.Fatalf("output = %s", out.String())
	}
	if _, err := os.Stat(root + "/.onlava/generated/temporal/typescript/manifest.json"); err != nil {
		t.Fatalf("expected generated manifest: %v", err)
	}
}

func TestParseTemporalDeploymentArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseTemporalDeploymentArgs("ramp", []string{
		"--app-root", "/tmp/app",
		"--deployment", "orders-api",
		"--build-id", "sha-123",
		"--percentage", "25",
		"--ignore-missing-task-queues",
		"--allow-no-pollers",
		"--json",
	})
	if err != nil {
		t.Fatalf("parseTemporalDeploymentArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Deployment != "orders-api" || opts.BuildID != "sha-123" || opts.Percentage != 25 || !opts.PercentageSet || !opts.IgnoreMissingTaskQueues || !opts.AllowNoPollers || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseTemporalDeploymentArgs("ramp", []string{"--build-id", "sha"}); err == nil {
		t.Fatal("expected missing percentage error")
	}
	if _, err := parseTemporalDeploymentArgs("set-current", []string{"--percentage", "10", "--build-id", "sha"}); err == nil {
		t.Fatal("expected percentage rejection")
	}
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		if _, err := parseTemporalDeploymentArgs("ramp", []string{"--build-id", "sha", "--percentage", value}); err == nil {
			t.Fatalf("expected non-finite percentage %q rejection", value)
		}
	}
	if _, err := parseTemporalDeploymentArgs("drain", []string{"--build-id", "sha", "--ignore-missing-task-queues"}); err == nil {
		t.Fatal("expected ignore-missing-task-queues drain rejection")
	}
	if _, err := parseTemporalDeploymentArgs("drain", []string{"--build-id", "sha", "--allow-no-pollers"}); err == nil {
		t.Fatal("expected allow-no-pollers drain rejection")
	}
}

func TestTemporalDeploymentCLIArgs(t *testing.T) {
	t.Setenv("TEMPORAL_API_KEY", "test-api-key")
	t.Setenv("TEMPORAL_TLS_CA_CERT_FILE", "/tmp/ca.pem")
	info := onlavaruntime.TemporalRuntimeInfo{
		Address:          "temporal.example:7233",
		Namespace:        "prod",
		DeploymentName:   "orders-api",
		APIKeyEnv:        "TEMPORAL_API_KEY",
		APIKeyEnvSet:     true,
		TLSEnabled:       true,
		TLSServerName:    "orders.tmprl.cloud",
		TLSServerNameSet: true,
		TLSCACertFileEnv: "TEMPORAL_TLS_CA_CERT_FILE",
		TLSCACertFileSet: true,
		ConnectTimeoutMS: onlavaruntime.DefaultTemporalConnectWait.Milliseconds(),
		WorkerBuildID:    "sha-123",
		WorkerBuildIDEnv: onlavaruntime.DefaultTemporalBuildIDEnv,
		DeploymentEnv:    onlavaruntime.DefaultTemporalDeploymentEnv,
		Versioning:       onlavaruntime.DefaultTemporalVersioning,
		VersioningEnv:    onlavaruntime.DefaultTemporalVersioningEnv,
		LocalDBFilename:  onlavaruntime.DefaultTemporalLocalDBFile,
		PayloadCodec:     onlavaruntime.DefaultTemporalPayloadCodec,
		TaskQueueEnv:     onlavaruntime.DefaultTemporalTaskQueueEnv,
		HostReporting:    true,
		HostReportingEnv: onlavaruntime.DefaultTemporalHostReportingEnv,
		AddressEnv:       onlavaruntime.DefaultTemporalAddressEnv,
		NamespaceEnvSet:  true,
		TaskQueuePrefix:  "onlava.orders",
		WorkerBuildIDSet: true,
		DeploymentEnvSet: true,
		VersioningEnvSet: true,
		HostReportingSet: true,
		LocalAutoStart:   false,
	}
	opts := temporalDeploymentOptions{
		BuildID:                 "sha-123",
		Percentage:              25,
		IgnoreMissingTaskQueues: true,
		AllowNoPollers:          true,
	}
	args := temporalDeploymentCLIArgs("ramp", opts, info)
	got := strings.Join(args, "\x00")
	for _, want := range []string{
		"worker\x00deployment\x00set-ramping-version",
		"--deployment-name\x00orders-api",
		"--build-id\x00sha-123",
		"--percentage\x0025",
		"--ignore-missing-task-queues",
		"--allow-no-pollers",
		"--address\x00temporal.example:7233",
		"--namespace\x00prod",
		"--api-key\x00test-api-key",
		"--tls",
		"--tls-server-name\x00orders.tmprl.cloud",
		"--tls-ca-path\x00/tmp/ca.pem",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("temporalDeploymentCLIArgs missing %q in %#v", want, args)
		}
	}
}

func TestRunWorkerBindingsWritesFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"orders"}`)
	writeTestAppFile(t, root, ".onlava/workers/email.json", `{
  "schema_version": "onlava.worker.manifest.v1",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "onlava-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["onlava.orders.activity.email.python"]},
  "activities": [{"name": "email.SendWelcome/v1", "input": "WelcomeEmail", "output": "Void"}]
}`)
	outDir := root + "/bindings"

	var out bytes.Buffer
	if err := runWorkerBindings(workerBindingsOptions{AppRoot: root, OutDir: outDir, JSON: true}, &out); err != nil {
		t.Fatalf("runWorkerBindings returned error: %v", err)
	}
	var payload struct {
		OK    bool `json:"ok"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Files) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if _, err := os.Stat(outDir + "/email/onlava_worker.py"); err != nil {
		t.Fatalf("expected generated python binding: %v", err)
	}
}

func TestRunConsoleJSONPhaseAndBanner(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &out, false, true, "jsonapp", "/repo/jsonapp")

	if err := console.Phase("Compiling application source code", func() error { return nil }); err != nil {
		t.Fatalf("Phase() error = %v", err)
	}
	console.Banner(runURLs{
		API:       "https://api.jsonapp.localhost",
		Dashboard: "https://console.jsonapp.localhost/jsonapp",
		MCP:       "https://mcp.jsonapp.localhost/sse?appID=jsonapp",
		Frontends: map[string]string{
			"web": "https://web.jsonapp.localhost",
		},
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
	t.Parallel()

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

func TestRunConsoleInitialBuildFailedEmitsRunFailed(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &out, false, true, "jsonapp", "/tmp/jsonapp")
	console.InitialBuildFailed(fmt.Errorf("compile failed"), runURLs{
		API:       "https://api.jsonapp.localhost",
		Dashboard: "https://console.jsonapp.localhost/jsonapp",
		MCP:       "https://mcp.jsonapp.localhost/sse?appID=jsonapp",
	})

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("event count = %d, want 2\n%s", len(lines), out.String())
	}
	var event runEvent
	if err := json.Unmarshal(lines[1], &event); err != nil {
		t.Fatalf("json.Unmarshal(run.failed): %v\n%s", err, lines[1])
	}
	if event.Type != "run.failed" {
		t.Fatalf("event type = %q, want run.failed", event.Type)
	}
	if event.Data["error"] != "compile failed" || event.Data["dashboard_url"] != "https://console.jsonapp.localhost/jsonapp" {
		t.Fatalf("run.failed data = %+v", event.Data)
	}
}

func TestRunConsoleJSONHidesVictoriaUnlessVerbose(t *testing.T) {
	t.Parallel()

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
