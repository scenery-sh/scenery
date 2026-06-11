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

	"scenery.sh/internal/app"
	sceneryruntime "scenery.sh/runtime"
)

func silenceCLIStderr(t *testing.T) {
	t.Helper()
	old := cliStderr
	cliStderr = io.Discard
	t.Cleanup(func() { cliStderr = old })
}

func TestParseServeArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseServeArgs([]string{"--port", "4444", "--listen", "0.0.0.0", "--app-root", "/tmp/app", "--env", "production", "--log-format", "json"})
	if err != nil {
		t.Fatalf("parseServeArgs returned error: %v", err)
	}
	if opts.Port != 4444 || opts.Listen != "0.0.0.0" || opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseServeArgsRejectsDevFlags(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--verbose", "--json", "--watch", "--dashboard", "--proxy"} {
		if _, err := parseServeArgs([]string{flag}); err == nil {
			t.Fatalf("parseServeArgs(%q) returned nil error", flag)
		}
	}
}

func TestParseDevArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDevArgs([]string{"--port", "4444", "--listen", "0.0.0.0", "--verbose", "--json", "--app-root", "/tmp/app", "--detach", "--claim-aliases"})
	if err != nil {
		t.Fatalf("parseDevArgs returned error: %v", err)
	}
	if opts.Port != 4444 || opts.Listen != "0.0.0.0" || !opts.PortSet || !opts.ListenSet || !opts.Verbose || !opts.JSON || opts.AppRoot != "/tmp/app" || !opts.Detach || !opts.ClaimAliases {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseDevArgsRejectsLegacyProxyFlags(t *testing.T) {
	t.Parallel()

	_, err := parseDevArgs([]string{"--proxy"})
	if err == nil || !strings.Contains(err.Error(), "scenery system edge install") {
		t.Fatalf("parseDevArgs(--proxy) error = %v, want trusted edge replacement hint", err)
	}
	if _, err := parseDevArgs([]string{"--trust", "--json"}); err == nil || !strings.Contains(err.Error(), "scenery system trust") {
		t.Fatalf("parseDevArgs(--trust) error = %v, want system trust migration hint", err)
	}
}

func TestParseDevArgsRejectsSessionSelection(t *testing.T) {
	t.Parallel()

	if _, err := parseDevArgs([]string{"--session", "review-a"}); err == nil || !strings.Contains(err.Error(), "one app root has one live dev runtime") {
		t.Fatalf("expected --session to fail with one-runtime guidance, got %v", err)
	}
	if _, err := parseDevArgs([]string{"--new-session"}); err == nil || !strings.Contains(err.Error(), "Git worktree") {
		t.Fatalf("expected --new-session to fail with worktree guidance, got %v", err)
	}
}

func TestDevCommandUsesWatcherPath(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if listen.Network != "tcp" || listen.Addr != "127.0.0.1:4444" || !listen.Explicit || !listen.ClaimAliases || !verbose || !jsonMode || appRoot != "/tmp/app" {
			t.Fatalf("watch args = %+v %v %v %q", listen, verbose, jsonMode, appRoot)
		}
		if got := getenvForTest("SCENERY_LOCAL_PROXY"); got != "" {
			t.Fatalf("SCENERY_LOCAL_PROXY = %q, want empty", got)
		}
		return nil
	}

	if err := devCommand([]string{"--port", "4444", "--verbose", "--json", "--app-root", "/tmp/app", "--claim-aliases"}); err != nil {
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
	t.Setenv("SCENERY_LOCAL_PROXY", "")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if listen.Addr != "" || listen.Network != "" || listen.Explicit || listen.PreferTCP {
			t.Fatalf("listen = %+v, want agent/private default", listen)
		}
		if got := getenvForTest("SCENERY_LOCAL_PROXY"); got != "" {
			t.Fatalf("SCENERY_LOCAL_PROXY = %q, want empty", got)
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
	t.Setenv("SCENERY_LOCAL_PROXY", "0")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("SCENERY_LOCAL_PROXY"); got != "0" {
			t.Fatalf("SCENERY_LOCAL_PROXY = %q, want 0", got)
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

func TestDevCommandRejectsLegacyProxyFlag(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("SCENERY_LOCAL_PROXY", "0")

	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		t.Fatal("watcher should not run when --proxy is rejected")
		return nil
	}

	err := devCommand([]string{"--proxy"})
	if err == nil || !strings.Contains(err.Error(), "legacy local proxy") {
		t.Fatalf("devCommand --proxy error = %v, want legacy proxy rejection", err)
	}
}

func TestDevCommandPreservesTrustSkipEnv(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("SCENERY_LOCAL_PROXY", "")
	t.Setenv("SCENERY_LOCAL_PROXY_SKIP_TRUST_INSTALL", "1")

	called := false
	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		called = true
		if got := getenvForTest("SCENERY_LOCAL_PROXY_SKIP_TRUST_INSTALL"); got != "1" {
			t.Fatalf("SCENERY_LOCAL_PROXY_SKIP_TRUST_INSTALL = %q, want 1", got)
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

func TestDevCommandRejectsLegacyProxyEnv(t *testing.T) {
	prev := runWithWatchFunc
	defer func() { runWithWatchFunc = prev }()
	t.Setenv("SCENERY_LOCAL_PROXY", "1")

	runWithWatchFunc = func(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
		t.Fatal("watcher should not run when SCENERY_LOCAL_PROXY=1 is rejected")
		return nil
	}

	err := devCommand([]string{})
	if err == nil || !strings.Contains(err.Error(), "SCENERY_LOCAL_PROXY") {
		t.Fatalf("devCommand with SCENERY_LOCAL_PROXY=1 error = %v, want env rejection", err)
	}
}

func getenvForTest(key string) string {
	return os.Getenv(key)
}

func TestServeCommandUsesHeadlessPath(t *testing.T) {
	prev := serveHeadlessFunc
	defer func() { serveHeadlessFunc = prev }()

	called := false
	serveHeadlessFunc = func(addr string, opts serveOptions) error {
		called = true
		if addr != "127.0.0.1:4444" || opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" {
			t.Fatalf("headless args = %q %+v", addr, opts)
		}
		return nil
	}

	if err := serveCommand([]string{"--port", "4444", "--app-root", "/tmp/app", "--env", "production", "--log-format", "json"}); err != nil {
		t.Fatalf("serveCommand returned error: %v", err)
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

	opts, err := parseWorkerArgs([]string{"--app-root", "/tmp/app", "--env", "production", "--log-format", "json", "--task-queue", "scenery.app.worker.go"})
	if err != nil {
		t.Fatalf("parseWorkerArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" || !reflect.DeepEqual(opts.TaskQueues, []string{"scenery.app.worker.go"}) {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseWorkerArgsSplitsRepeatedTaskQueues(t *testing.T) {
	t.Parallel()

	opts, err := parseWorkerArgs([]string{"--task-queue", "scenery.app.worker.go, scenery.app.email.go", "--task-queue", "scenery.app.worker.go"})
	if err != nil {
		t.Fatalf("parseWorkerArgs returned error: %v", err)
	}
	want := []string{"scenery.app.worker.go", "scenery.app.email.go", "scenery.app.worker.go"}
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
		if opts.AppRoot != "/tmp/app" || opts.Env != "production" || opts.LogFormat != "json" || !reflect.DeepEqual(opts.TaskQueues, []string{"scenery.app.worker.go"}) {
			t.Fatalf("worker opts = %+v", opts)
		}
		return nil
	}

	if err := workerCommand([]string{"--app-root", "/tmp/app", "--env", "production", "--log-format", "json", "--task-queue", "scenery.app.worker.go"}); err != nil {
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
	writeTestAppFile(t, root, ".scenery.json", `{"name":"orders"}`)
	writeTestAppFile(t, root, "house/preview.worker.ts", `import { activity } from "scenery/worker";
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
	if !strings.Contains(out.String(), ".scenery/generated/temporal/typescript/worker.ts") {
		t.Fatalf("output = %s", out.String())
	}
	if _, err := os.Stat(root + "/.scenery/generated/temporal/typescript/manifest.json"); err != nil {
		t.Fatalf("expected generated manifest: %v", err)
	}
}

func TestRunWorkerTypeScriptRequiresTemporalEnabledToRun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"orders","temporal":{"typescript":{"enabled":true}}}`)
	writeTestAppFile(t, root, "house/preview.worker.ts", `import { activity } from "scenery/worker";
export type RenderInput = { id: string };
export type RenderOutput = { url: string };
export const render = activity<RenderInput, RenderOutput>({
  name: "house.Render/v1",
  taskQueue: "onlv.house.preview.ts"
}, async (_ctx, input) => ({ url: input.id }));
`)
	var out bytes.Buffer
	err := runWorkerTypeScript(workerTypeScriptOptions{AppRoot: root, TaskQueues: []string{"onlv.house.preview.ts"}}, &out)
	if err == nil || !strings.Contains(err.Error(), "temporal.enabled=true") {
		t.Fatalf("runWorkerTypeScript error = %v, want temporal.enabled gate", err)
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
	info := sceneryruntime.TemporalRuntimeInfo{
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
		ConnectTimeoutMS: sceneryruntime.DefaultTemporalConnectWait.Milliseconds(),
		WorkerBuildID:    "sha-123",
		WorkerBuildIDEnv: sceneryruntime.DefaultTemporalBuildIDEnv,
		DeploymentEnv:    sceneryruntime.DefaultTemporalDeploymentEnv,
		Versioning:       sceneryruntime.DefaultTemporalVersioning,
		VersioningEnv:    sceneryruntime.DefaultTemporalVersioningEnv,
		LocalDBFilename:  sceneryruntime.DefaultTemporalLocalDBFile,
		PayloadCodec:     sceneryruntime.DefaultTemporalPayloadCodec,
		TaskQueueEnv:     sceneryruntime.DefaultTemporalTaskQueueEnv,
		HostReporting:    true,
		HostReportingEnv: sceneryruntime.DefaultTemporalHostReportingEnv,
		AddressEnv:       sceneryruntime.DefaultTemporalAddressEnv,
		NamespaceEnvSet:  true,
		TaskQueuePrefix:  "scenery.orders",
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
	writeTestAppFile(t, root, ".scenery.json", `{"name":"orders"}`)
	writeTestAppFile(t, root, ".scenery/workers/email.json", `{
  "schema_version": "scenery.worker.manifest.v1",
  "app": "orders",
  "language": "python",
  "build_id": "sha-python",
  "payload_codec": "scenery-json-v1",
  "temporal": {"namespace": "default", "task_queues": ["scenery.orders.activity.email.python"]},
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
	if _, err := os.Stat(outDir + "/email/scenery_worker.py"); err != nil {
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
	if first.SchemaVersion != "scenery.run.event.v1" || first.Type != "phase.start" {
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
	if !bytes.Contains(verboseOut.Bytes(), []byte("VictoriaMetrics:")) || !bytes.Contains(verboseOut.Bytes(), []byte("http://127.0.0.1:8428")) {
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
