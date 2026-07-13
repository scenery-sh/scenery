package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func TestVictoriaEnabledDefaultsToTrue(t *testing.T) {
	t.Setenv("SCENERY_DEV_VICTORIA", "")
	if !victoriaEnabled() {
		t.Fatal("victoriaEnabled() = false, want true")
	}
}

func TestVictoriaEnabledCanBeDisabled(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SCENERY_DEV_VICTORIA", value)
			if victoriaEnabled() {
				t.Fatalf("victoriaEnabled() with %q = true, want false", value)
			}
		})
	}
}

func TestVictoriaArchiveName(t *testing.T) {
	t.Parallel()

	name, err := victoriaArchiveName(victoriaComponentSpec{
		ArchiveSlug: "victoria-traces",
		Version:     "v0.8.1",
	})
	if err != nil {
		t.Fatalf("victoriaArchiveName: %v", err)
	}
	if !strings.HasPrefix(name, "victoria-traces-") || !strings.HasSuffix(name, "-v0.8.1.tar.gz") {
		t.Fatalf("archive name = %q", name)
	}
}

func TestChecksumForArchive(t *testing.T) {
	t.Parallel()

	body := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  victoria-traces-linux-amd64-v0.8.1.tar.gz\n"
	got := checksumForArchive(body, "victoria-traces-linux-amd64-v0.8.1.tar.gz")
	if got != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("checksum = %q", got)
	}
}

func TestResolveVictoriaBinaryPrefersExplicitEnv(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "victoria-traces-prod")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	spec := victoriaComponentSpec{
		BinaryName: "victoria-traces-prod",
		EnvPrefix:  "SCENERY_VICTORIA_TRACES",
	}
	t.Setenv("SCENERY_VICTORIA_TRACES_BIN", binary)

	got, err := resolveVictoriaBinary(context.Background(), spec, filepath.Join(dir, "bin"), false)
	if err != nil {
		t.Fatalf("resolveVictoriaBinary: %v", err)
	}
	if got != binary {
		t.Fatalf("binary = %q, want %q", got, binary)
	}
}

func TestResolveVictoriaBinaryIgnoresPathBinary(t *testing.T) {
	dir := t.TempDir()
	pathBinary := filepath.Join(dir, "victoria-traces-prod")
	if err := os.WriteFile(pathBinary, []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	spec := victoriaComponentSpec{
		DisplayName: "VictoriaTraces",
		ArchiveSlug: "victoria-traces",
		BinaryName:  "victoria-traces-prod",
		EnvPrefix:   "SCENERY_VICTORIA_TRACES",
	}
	_, err := resolveVictoriaBinary(context.Background(), spec, filepath.Join(t.TempDir(), "bin"), false)
	if err == nil || !strings.Contains(err.Error(), "system PATH binaries are not used") {
		t.Fatalf("resolveVictoriaBinary err = %v", err)
	}
}

func TestVictoriaStackEnv(t *testing.T) {
	t.Parallel()

	stack := &victoriaStack{components: []*victoriaComponent{
		{
			spec: victoriaComponentSpec{
				OTELVar:            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
				SceneryURLVar:      "SCENERY_VICTORIA_TRACES_URL",
				SceneryEndpointVar: "SCENERY_VICTORIA_TRACES_ENDPOINT",
			},
			baseURL:     "http://127.0.0.1:10428",
			endpointURL: "http://127.0.0.1:10428/insert/opentelemetry/v1/traces",
		},
	}}

	env := stack.Env()
	if !containsString(env, "SCENERY_DEV_OBSERVABILITY_BACKEND=victoria") {
		t.Fatalf("env missing backend marker: %v", env)
	}
	if !containsString(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:10428/insert/opentelemetry/v1/traces") {
		t.Fatalf("env missing OTLP endpoint: %v", env)
	}
}

func TestVictoriaStackSubstrateRoundTrip(t *testing.T) {
	t.Parallel()

	stack := &victoriaStack{}
	for _, spec := range victoriaComponentSpecs() {
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", spec.DefaultPort)
		stack.components = append(stack.components, &victoriaComponent{
			spec:        spec,
			baseURL:     baseURL,
			endpointURL: baseURL + spec.EndpointPath,
		})
	}
	req := stack.SubstrateRequest(123)
	if req.Kind != localagent.SubstrateVictoria || req.OwnerPID != 123 {
		t.Fatalf("substrate request = %+v", req)
	}
	substrate := localagent.Substrate{
		Kind:      req.Kind,
		URLs:      req.URLs,
		Endpoints: req.Endpoints,
	}
	roundTrip := victoriaStackFromSubstrate(substrate)
	if roundTrip == nil {
		t.Fatal("expected stack from substrate")
		return
	}
	env := roundTrip.Env()
	if !containsString(env, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:8428/opentelemetry/v1/metrics") {
		t.Fatalf("env = %+v", env)
	}
	urls := roundTrip.URLs()
	if urls["metrics"] != "http://127.0.0.1:8428/vmui" {
		t.Fatalf("urls = %+v", urls)
	}
	roundTrip.MarkExternal()
	if !roundTrip.components[0].external {
		t.Fatal("component not marked external")
	}
}

func TestVictoriaStackFromSubstrateRequiresAllComponents(t *testing.T) {
	t.Parallel()

	substrate := localagent.Substrate{
		Kind: localagent.SubstrateVictoria,
		URLs: map[string]string{
			"metrics": "http://127.0.0.1:8428",
		},
		Endpoints: map[string]string{
			"metrics": "http://127.0.0.1:8428/opentelemetry/v1/metrics",
		},
	}
	if stack := victoriaStackFromSubstrate(substrate); stack != nil {
		t.Fatalf("expected incomplete Victoria substrate to be rejected: %+v", stack)
	}
}

func TestEnsureSharedVictoriaStackReusesAgentSubstrate(t *testing.T) {
	t.Parallel()

	ctx, client := startSubstrateTestAgent(t)
	ownerPID := startFakeSubstrateOwner(t)
	substrate := reachableVictoriaTestSubstrate(t, ownerPID)
	created, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:      substrate.Kind,
		Status:    substrate.Status,
		OwnerPID:  substrate.OwnerPID,
		PIDs:      substrate.PIDs,
		URLs:      substrate.URLs,
		Endpoints: substrate.Endpoints,
	})
	if err != nil {
		t.Fatal(err)
	}
	stack, reused, err := (&devSupervisor{agent: client}).ensureSharedVictoriaStack(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if stack == nil || !reused {
		t.Fatalf("ensure result stack=%T reused=%v", stack, reused)
	}
	after, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at changed on reuse: before=%s after=%s", created.CreatedAt, after.CreatedAt)
	}
	for _, component := range stack.components {
		if component == nil || !component.external {
			t.Fatalf("reused component not marked external: %+v", component)
		}
	}
}

func TestEnsureSharedVictoriaStackReplacesStaleOwner(t *testing.T) {
	configureManagedVictoriaTestProcesses(t)

	ctx, client := startSubstrateTestAgent(t)
	stale, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateVictoria,
		Status:   "ready",
		OwnerPID: 99999991,
	})
	if err != nil {
		t.Fatal(err)
	}

	stack, reused, err := (&devSupervisor{agent: client}).ensureSharedVictoriaStack(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if stack == nil || reused {
		t.Fatalf("ensure result stack=%T reused=%v", stack, reused)
	}
	fresh, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.OwnerPID <= 0 || !containsPID(fresh.PIDs, fresh.OwnerPID) {
		t.Fatalf("fresh owner pid = %d, component pids = %v", fresh.OwnerPID, fresh.PIDs)
	}
	if fresh.CreatedAt.Equal(stale.CreatedAt) {
		t.Fatalf("stale substrate was updated in place: created_at=%s", fresh.CreatedAt)
	}
}

func TestEnsureSharedVictoriaStackRejectsUnverifiedLiveOwner(t *testing.T) {
	configureManagedVictoriaTestProcesses(t)
	ctx, client := startSubstrateTestAgent(t)
	ownerPID := startFakeSubstrateOwner(t)
	badOwner := localagent.Owner{PID: ownerPID, StartedAt: "not-the-live-start-time"}
	pids := map[string]int{}
	owners := map[string]localagent.Owner{}
	for _, spec := range victoriaComponentSpecs() {
		pids[spec.Name] = ownerPID
		owners[spec.Name] = badOwner
	}
	before, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateVictoria,
		Status:   "degraded",
		OwnerPID: ownerPID,
		Owner:    badOwner,
		PIDs:     pids,
		Owners:   owners,
	})
	if err != nil {
		t.Fatal(err)
	}
	stack, reused, err := (&devSupervisor{agent: client}).ensureSharedVictoriaStack(ctx, t.TempDir())
	if err == nil || stack != nil || reused {
		t.Fatalf("ensure stack=%T reused=%v err=%v, want ownership rejection", stack, reused, err)
	}
	after, getErr := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) || after.OwnerPID != ownerPID {
		t.Fatalf("unverified substrate was replaced: before=%+v after=%+v", before, after)
	}
}

func TestEnsureSharedVictoriaStackSerializesConcurrentStarts(t *testing.T) {
	configureManagedVictoriaTestProcesses(t)

	ctx, client := startSubstrateTestAgent(t)
	supervisor := &devSupervisor{agent: client}
	root := t.TempDir()
	unlock := lockVictoriaSubstrateProcess(root)
	released := false
	t.Cleanup(func() {
		if !released {
			unlock()
		}
	})

	type ensureResult struct {
		stack  *victoriaStack
		reused bool
		err    error
	}
	results := make(chan ensureResult, 2)
	for range 2 {
		go func() {
			stack, reused, err := supervisor.ensureSharedVictoriaStack(ctx, root)
			results <- ensureResult{stack: stack, reused: reused, err: err}
		}()
	}
	select {
	case result := <-results:
		t.Fatalf("ensure returned while the process lock was held: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	released = true

	started, reused := 0, 0
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.stack == nil {
				t.Fatal("ensure returned a nil Victoria stack")
			}
			if result.reused {
				reused++
			} else {
				started++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent Victoria starts")
		}
	}
	if started != 1 || reused != 1 {
		t.Fatalf("concurrent ensure results started=%d reused=%d, want 1 each", started, reused)
	}
}

func TestVictoriaSubstrateMonitorRecordsExitState(t *testing.T) {
	t.Parallel()

	ctx, client := startSubstrateTestAgent(t)
	done := make(chan error, 1)
	stack := &victoriaStack{components: []*victoriaComponent{{
		spec:        victoriaComponentSpec{Name: "logs", DisplayName: "VictoriaLogs"},
		baseURL:     "http://127.0.0.1:1",
		endpointURL: "http://127.0.0.1:1/insert/opentelemetry/v1/logs",
		stdoutLog:   "/tmp/victoria.logs.stdout.log",
		stderrLog:   "/tmp/victoria.logs.stderr.log",
		done:        done,
		startedAt:   time.Now().Add(-time.Second).UTC(),
	}}}
	if _, err := client.UpsertSubstrate(ctx, stack.SubstrateRequest(os.Getpid())); err != nil {
		t.Fatal(err)
	}
	monitorDone := monitorVictoriaSubstrate(t.TempDir(), client, nil, stack)
	done <- fmt.Errorf("exit status 7")
	close(done)
	substrate := waitForSubstrateStatus(t, ctx, client, localagent.SubstrateVictoria, "degraded")
	if substrate.LastExit == nil || substrate.LastExit.Component != "logs" {
		t.Fatalf("last exit = %+v", substrate.LastExit)
	}
	if got := substrate.ComponentExits["logs"].Component; got != "logs" {
		t.Fatalf("component exit = %+v", substrate.ComponentExits)
	}
	waitForMonitorDone(t, monitorDone)
}

func TestVictoriaSupervisorRecoversExitedComponent(t *testing.T) {
	configureManagedVictoriaTestProcesses(t)
	ctx, client := startSubstrateTestAgent(t)
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	root := t.TempDir()
	supervisor := &devSupervisor{ctx: runCtx, cancel: cancel, agent: client}
	stack, reused, err := supervisor.ensureSharedVictoriaStack(runCtx, root)
	if err != nil || stack == nil || reused {
		t.Fatalf("initial ensure stack=%T reused=%v err=%v", stack, reused, err)
	}
	supervisor.victoria = stack
	monitorVictoriaSubstrate(root, client, nil, stack)
	recoveryDone := supervisor.monitorVictoriaRecovery(root, 10*time.Millisecond, 50*time.Millisecond)

	before, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	logs := victoriaTestComponent(t, stack, "logs")
	if err := logs.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	after := waitForVictoriaPIDChange(t, ctx, client, "logs", before.PIDs["logs"])
	if after.Status != "ready" || len(after.PIDs) != len(victoriaComponentSpecs()) {
		t.Fatalf("recovered substrate = %+v", after)
	}
	for name, pid := range before.PIDs {
		if after.PIDs[name] == pid {
			t.Fatalf("Victoria component %s was not replaced with the stack: before=%v after=%v", name, before.PIDs, after.PIDs)
		}
	}
	substrates, err := client.ListSubstrates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(substrates) != 1 || substrates[0].Kind != localagent.SubstrateVictoria {
		t.Fatalf("registered substrates = %+v, want one Victoria stack", substrates)
	}
	cancel()
	waitForMonitorDone(t, recoveryDone)
}

func TestVictoriaRecoverySerializesConcurrentAttempts(t *testing.T) {
	configureManagedVictoriaTestProcesses(t)
	ctx, client := startSubstrateTestAgent(t)
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	root := t.TempDir()
	supervisor := &devSupervisor{ctx: runCtx, cancel: cancel, agent: client}
	stack, _, err := supervisor.ensureSharedVictoriaStack(runCtx, root)
	if err != nil {
		t.Fatal(err)
	}
	monitorVictoriaSubstrate(root, client, nil, stack)
	before, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	if err := victoriaTestComponent(t, stack, "logs").cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitForVictoriaPortAvailable(t, victoriaComponentSpecs()[1].DefaultPort)

	type ensureResult struct {
		stack  *victoriaStack
		reused bool
		err    error
	}
	results := make(chan ensureResult, 2)
	for range 2 {
		go func() {
			stack, reused, err := supervisor.ensureSharedVictoriaStack(runCtx, root)
			results <- ensureResult{stack: stack, reused: reused, err: err}
		}()
	}
	started, reused := 0, 0
	for range 2 {
		result := <-results
		if result.err != nil || result.stack == nil {
			t.Fatalf("concurrent recovery stack=%T reused=%v err=%v", result.stack, result.reused, result.err)
		}
		if result.reused {
			reused++
		} else {
			started++
		}
	}
	if started != 1 || reused != 1 {
		t.Fatalf("concurrent recoveries started=%d reused=%d, want one each", started, reused)
	}
	after := waitForVictoriaPIDChange(t, ctx, client, "logs", before.PIDs["logs"])
	if len(after.PIDs) != len(victoriaComponentSpecs()) {
		t.Fatalf("recovered PIDs = %v", after.PIDs)
	}
}

func TestVictoriaRecoveryStopsWithSupervisor(t *testing.T) {
	configureManagedVictoriaTestProcesses(t)
	ctx, client := startSubstrateTestAgent(t)
	runCtx, cancel := context.WithCancel(ctx)
	root := t.TempDir()
	supervisor := &devSupervisor{ctx: runCtx, cancel: cancel, agent: client}
	stack, _, err := supervisor.ensureSharedVictoriaStack(runCtx, root)
	if err != nil {
		t.Fatal(err)
	}
	supervisor.victoria = stack
	before, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	done := supervisor.monitorVictoriaRecovery(root, 10*time.Millisecond, 50*time.Millisecond)
	cancel()
	waitForMonitorDone(t, done)
	time.Sleep(75 * time.Millisecond)
	after, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	for name, pid := range before.PIDs {
		if after.PIDs[name] != pid {
			t.Fatalf("Victoria restarted after supervisor shutdown: before=%v after=%v", before.PIDs, after.PIDs)
		}
	}
}

func TestVictoriaRecoveryFailureIsVisible(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	ctx, client := startSubstrateTestAgent(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	_ = listener.Close()
	urls := map[string]string{}
	endpoints := map[string]string{}
	for _, spec := range victoriaComponentSpecs() {
		urls[spec.Name] = baseURL
		endpoints[spec.Name] = baseURL + spec.EndpointPath
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:      localagent.SubstrateVictoria,
		Status:    "ready",
		OwnerPID:  os.Getpid(),
		URLs:      urls,
		Endpoints: endpoints,
	}); err != nil {
		t.Fatal(err)
	}

	var humanOut, humanErr bytes.Buffer
	console := newRunConsole(&humanOut, &humanErr, false, false, "demo", t.TempDir())
	supervisor := &devSupervisor{ctx: ctx, agent: client, console: console, victoriaStarted: true}
	supervisor.reportVictoriaRecoveryFailure(t.TempDir(), fmt.Errorf("owner fingerprint mismatch"), 2*time.Second)
	if got := humanErr.String(); !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "ERR Victoria observability recovery failed; retrying in 2.0s: owner fingerprint mismatch") {
		t.Fatalf("human recovery warning = %q", got)
	}
	substrate, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	if substrate.Status != "degraded" {
		t.Fatalf("substrate status = %q, want degraded", substrate.Status)
	}

	var jsonOut bytes.Buffer
	jsonConsole := newRunConsole(&jsonOut, &bytes.Buffer{}, false, true, "demo", t.TempDir())
	jsonSupervisor := &devSupervisor{ctx: ctx, console: jsonConsole, victoriaStarted: true}
	jsonSupervisor.reportVictoriaRecoveryFailure("", fmt.Errorf("agent unavailable"), 4*time.Second)
	if got := jsonOut.String(); !strings.Contains(got, `"type":"process.output"`) || !strings.Contains(got, "ERR Victoria observability recovery failed; retrying in 4.0s: agent unavailable") {
		t.Fatalf("detached recovery event = %q", got)
	}
}

func reachableVictoriaTestSubstrate(t *testing.T, ownerPID int) localagent.Substrate {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)
	urls := make(map[string]string)
	endpoints := make(map[string]string)
	pids := make(map[string]int)
	for _, spec := range victoriaComponentSpecs() {
		urls[spec.Name] = server.URL
		endpoints[spec.Name] = server.URL + spec.EndpointPath
		pids[spec.Name] = ownerPID
	}
	return localagent.Substrate{
		Kind:      localagent.SubstrateVictoria,
		Status:    "ready",
		OwnerPID:  ownerPID,
		PIDs:      pids,
		URLs:      urls,
		Endpoints: endpoints,
	}
}

func configureManagedVictoriaTestProcesses(t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "victoria-test-process")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec \"$SCENERY_VICTORIA_TEST_BINARY\" -test.run='^TestVictoriaManagedProcessHelper$' -- \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCENERY_VICTORIA_TEST_BINARY", executable)
	t.Setenv("SCENERY_VICTORIA_PROCESS_HELPER", "1")
	listeners := make([]net.Listener, 0, len(victoriaComponentSpecs()))
	for _, spec := range victoriaComponentSpecs() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		t.Setenv(spec.EnvPrefix+"_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
		t.Setenv(spec.EnvPrefix+"_BIN", script)
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	t.Setenv("SCENERY_DEV_VICTORIA", "1")
	t.Setenv("SCENERY_DEV_VICTORIA_DOWNLOAD", "0")
}

func TestVictoriaManagedProcessHelper(t *testing.T) {
	if os.Getenv("SCENERY_VICTORIA_PROCESS_HELPER") != "1" {
		return
	}
	addr := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-httpListenAddr=") {
			addr = strings.TrimPrefix(arg, "-httpListenAddr=")
		}
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(2)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(0)
		}
		_ = conn.Close()
	}
}

func victoriaTestComponent(t *testing.T, stack *victoriaStack, name string) *victoriaComponent {
	t.Helper()
	for _, component := range stack.components {
		if component != nil && component.spec.Name == name {
			return component
		}
	}
	t.Fatalf("Victoria component %q not found", name)
	return nil
}

func containsPID(pids map[string]int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}
	return false
}

func waitForVictoriaPIDChange(t *testing.T, ctx context.Context, client *localagent.Client, component string, previous int) localagent.Substrate {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		substrate, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
		if err == nil && substrate.Status == "ready" && substrate.PIDs[component] > 0 && substrate.PIDs[component] != previous {
			return substrate
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Victoria component %s PID did not change from %d", component, previous)
	return localagent.Substrate{}
}

func waitForVictoriaPortAvailable(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tcpAddrAvailable(victoriaDefaultHost, port) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Victoria port %d did not become available", port)
}

func TestURLAcceptsTCP(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	if !urlAcceptsTCP(server.URL) {
		t.Fatalf("urlAcceptsTCP(%q) = false, want true", server.URL)
	}
	if urlAcceptsTCP("http://127.0.0.1:1") {
		t.Fatal("urlAcceptsTCP on closed port = true, want false")
	}
}

func TestStartVictoriaComponentReusesOccupiedPort(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	component, err := startVictoriaComponent(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "bin"), victoriaComponentSpec{
		Name:               "traces",
		DisplayName:        "VictoriaTraces",
		DefaultPort:        port,
		EndpointPath:       "/insert/opentelemetry/v1/traces",
		StorageDir:         "traces-data",
		OTELVar:            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		SceneryURLVar:      "SCENERY_VICTORIA_TRACES_URL",
		SceneryEndpointVar: "SCENERY_VICTORIA_TRACES_ENDPOINT",
	}, false, nil)
	if err != nil {
		t.Fatalf("startVictoriaComponent: %v", err)
	}
	if !component.external {
		t.Fatal("component.external = false, want true")
	}
	if component.endpointURL == "" {
		t.Fatal("component endpoint URL is empty")
	}
}

func TestStartVictoriaComponentsAttributesStartErrors(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "victoria-logs-prod")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCENERY_VICTORIA_LOGS_BIN", bin)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	results := startVictoriaComponents(context.Background(), root, filepath.Join(root, "bin"), []victoriaComponentSpec{
		{
			Name:         "metrics",
			DisplayName:  "VictoriaMetrics",
			DefaultPort:  ln.Addr().(*net.TCPAddr).Port,
			EndpointPath: "/opentelemetry/v1/metrics",
			StorageDir:   "metrics-data",
		},
		{
			Name:         "logs",
			DisplayName:  "VictoriaLogs",
			DefaultPort:  freeTestTCPPort(t),
			EndpointPath: "/insert/opentelemetry/v1/logs",
			StorageDir:   "logs-data",
			EnvPrefix:    "SCENERY_VICTORIA_LOGS",
		},
	}, false, nil)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].err != nil || results[0].component == nil || !results[0].component.external {
		t.Fatalf("occupied component result = %+v", results[0])
	}
	if results[1].err == nil || !strings.Contains(results[1].err.Error(), "VictoriaLogs exited before accepting TCP connections") {
		t.Fatalf("start error = %v, want VictoriaLogs attribution", results[1].err)
	}
}

func freeTestTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestBuildOTLPTracePayload(t *testing.T) {
	t.Parallel()

	endpoint := "Hello"
	payload := buildOTLPTracePayload(&devdash.TraceSummary{
		AppID:         "app",
		SessionID:     "session-a",
		AppRootHash:   "root123",
		Branch:        "feature/a",
		Worktree:      "onlv-a",
		TraceID:       "00000000000000010000000000000002",
		SpanID:        "0000000000000003",
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     time.Unix(1, 2).UTC(),
		DurationNanos: uint64(10 * time.Millisecond),
		ServiceName:   "svc",
		EndpointName:  &endpoint,
	}, []*devdash.TraceEvent{
		{
			TraceID:     "00000000000000010000000000000002",
			SpanID:      "0000000000000003",
			SessionID:   "session-a",
			AppRootHash: "root123",
			Branch:      "feature/a",
			Worktree:    "onlv-a",
			EventID:     4,
			EventTime:   time.Unix(1, 3).UTC(),
			Event: map[string]any{
				"span_event": map[string]any{"db": "query"},
			},
		},
	})

	resourceSpans := payload["resourceSpans"].([]any)
	scopeSpans := resourceSpans[0].(map[string]any)["scopeSpans"].([]any)
	spans := scopeSpans[0].(map[string]any)["spans"].([]any)
	span := spans[0].(map[string]any)
	if span["traceId"] != "00000000000000010000000000000002" {
		t.Fatalf("traceId = %v", span["traceId"])
	}
	if span["spanId"] != "0000000000000003" {
		t.Fatalf("spanId = %v", span["spanId"])
	}
	if span["name"] != "svc.Hello" {
		t.Fatalf("name = %v", span["name"])
	}
	if len(span["events"].([]any)) != 1 {
		t.Fatalf("events = %v", span["events"])
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"scenery.session_id", "session-a", "scenery.app_root_hash", "root123", "scenery.branch", "feature/a", "scenery.worktree", "onlv-a"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("payload missing %q: %s", want, data)
		}
	}
}

func TestBuildOTLPLogPayloadIncludesTraceContext(t *testing.T) {
	t.Parallel()

	payload := buildOTLPLogPayload(&devdash.LogEvent{
		AppID:       "app",
		SessionID:   "session-a",
		AppRootHash: "root123",
		Branch:      "feature/a",
		Worktree:    "onlv-a",
		TraceID:     "00000000000000010000000000000002",
		SpanID:      "0000000000000003",
		Level:       "info",
		Message:     "hello",
		Timestamp:   time.Unix(1, 2).UTC(),
	})

	resourceLogs := payload["resourceLogs"].([]any)
	scopeLogs := resourceLogs[0].(map[string]any)["scopeLogs"].([]any)
	records := scopeLogs[0].(map[string]any)["logRecords"].([]any)
	record := records[0].(map[string]any)
	if record["traceId"] != "00000000000000010000000000000002" {
		t.Fatalf("traceId = %v", record["traceId"])
	}
	if record["severityText"] != "INFO" {
		t.Fatalf("severityText = %v", record["severityText"])
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"scenery.session_id", "session-a", "scenery.app_root_hash", "root123", "scenery.branch", "feature/a", "scenery.worktree", "onlv-a"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("payload missing %q: %s", want, data)
		}
	}
}

func TestMetricAttributePairsIncludesSessionLabels(t *testing.T) {
	t.Parallel()

	attrs := metricAttributePairs(&devdash.TraceSummary{
		AppID:       "app",
		SessionID:   "session-a",
		AppRootHash: "root123",
		Branch:      "feature/a",
		Worktree:    "onlv-a",
		Type:        "REQUEST",
		IsRoot:      true,
		ServiceName: "svc",
	})
	got := map[string]any{}
	for _, attr := range attrs {
		got[attr.Key] = attr.Value
	}
	for key, want := range map[string]any{
		"scenery_session_id":    "session-a",
		"scenery_app_root_hash": "root123",
		"scenery_branch":        "feature/a",
		"scenery_worktree":      "onlv-a",
	} {
		if got[key] != want {
			t.Fatalf("metric attrs[%s] = %v, want %v; attrs=%+v", key, got[key], want, attrs)
		}
	}
}

func TestVictoriaQueryTraceSummariesFromJaegerAPI(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/jaeger/api/traces" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Fatalf("limit = %q, want 10", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{
					"traceID": "00000000000000010000000000000002",
					"processes": map[string]any{
						"p1": map[string]any{"serviceName": "app"},
					},
					"spans": []any{
						map[string]any{
							"traceID":       "00000000000000010000000000000002",
							"spanID":        "0000000000000003",
							"operationName": "svc.Hello",
							"startTime":     time.Unix(10, 0).UnixMicro(),
							"duration":      int64(25_000),
							"processID":     "p1",
							"tags": []any{
								map[string]any{"key": "scenery.service", "type": "string", "value": "svc"},
								map[string]any{"key": "scenery.endpoint", "type": "string", "value": "Hello"},
								map[string]any{"key": "scenery.session_id", "type": "string", "value": "session-a"},
								map[string]any{"key": "scenery.is_error", "type": "bool", "value": false},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	stack := &victoriaStack{components: []*victoriaComponent{{
		spec:    victoriaComponentSpec{Name: "traces"},
		baseURL: server.URL,
	}}}
	items, err := stack.QueryTraceSummaries(context.Background(), devdash.TraceQuery{
		AppID:     "app",
		SessionID: "session-a",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("QueryTraceSummaries: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ServiceName != "svc" || items[0].EndpointName == nil || *items[0].EndpointName != "Hello" {
		t.Fatalf("summary = %+v", items[0])
	}
	if items[0].SessionID != "session-a" {
		t.Fatalf("session = %q", items[0].SessionID)
	}
	if items[0].DurationNanos != uint64(25*time.Millisecond) {
		t.Fatalf("duration = %d", items[0].DurationNanos)
	}
}

func TestVictoriaQueryTraceSummariesClampsJaegerLimit(t *testing.T) {
	t.Parallel()

	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{
					"traceID":   "00000000000000010000000000000002",
					"processes": map[string]any{"p1": map[string]any{"serviceName": "app"}},
					"spans": []any{
						map[string]any{
							"traceID":       "00000000000000010000000000000002",
							"spanID":        "0000000000000003",
							"operationName": "svc.Hello",
							"startTime":     time.Unix(10, 0).UnixMicro(),
							"duration":      int64(25_000),
							"processID":     "p1",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	stack := &victoriaStack{components: []*victoriaComponent{{
		spec:    victoriaComponentSpec{Name: "traces"},
		baseURL: server.URL,
	}}}
	if _, err := stack.QueryTraceSummaries(context.Background(), devdash.TraceQuery{
		AppID: "app",
		Limit: 10000,
	}); err != nil {
		t.Fatalf("QueryTraceSummaries: %v", err)
	}
	if gotLimit != "1000" {
		t.Fatalf("limit = %q, want 1000", gotLimit)
	}
}

func TestVictoriaMarkClearedFiltersOlderTraces(t *testing.T) {
	t.Parallel()

	stack := &victoriaStack{}
	clearedAt := time.Unix(20, 0).UTC()
	stack.MarkCleared("app", clearedAt)
	if got := stack.ClearedAt("app"); !got.Equal(clearedAt) {
		t.Fatalf("ClearedAt = %s, want %s", got, clearedAt)
	}
}
