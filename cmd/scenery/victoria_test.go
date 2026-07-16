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
	"scenery.sh/internal/netprobe"
	"scenery.sh/internal/victoria"
)

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
	for _, component := range stack.Components() {
		if component == nil || !component.External() {
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
	killVictoriaTestStack(t, stack)
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
	for _, spec := range victoria.ComponentSpecs() {
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
		stack  *victoria.Stack
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
			killVictoriaTestStack(t, result.stack)
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
	stack := victoria.NewStack(victoria.ExternalComponent{
		Name:        "logs",
		DisplayName: "VictoriaLogs",
		BaseURL:     "http://127.0.0.1:1",
		EndpointURL: "http://127.0.0.1:1/insert/opentelemetry/v1/logs",
		StdoutLog:   "/tmp/victoria.logs.stdout.log",
		StderrLog:   "/tmp/victoria.logs.stderr.log",
		StartedAt:   time.Now().Add(-time.Second).UTC(),
		Done:        done,
	})
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
	killVictoriaTestComponent(t, stack, "logs")
	after := waitForVictoriaPIDChange(t, ctx, client, "logs", before.PIDs["logs"])
	if after.Status != "ready" || len(after.PIDs) != len(victoria.ComponentSpecs()) {
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
	killVictoriaTestComponent(t, stack, "logs")
	waitForVictoriaPortAvailable(t, victoria.ComponentSpecs()[1].DefaultPort)

	type ensureResult struct {
		stack  *victoria.Stack
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
	if len(after.PIDs) != len(victoria.ComponentSpecs()) {
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
	for _, spec := range victoria.ComponentSpecs() {
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
	for _, spec := range victoria.ComponentSpecs() {
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
	listeners := make([]net.Listener, 0, len(victoria.ComponentSpecs()))
	for _, spec := range victoria.ComponentSpecs() {
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

// killVictoriaTestStack reaps stand-in Victoria processes at test end. Once a
// shared stack is registered it is marked external, so Stack.Interrupt and
// context cancellation both leave its processes alive; without an explicit
// PID kill every registered test stack outlives the test binary.
func killVictoriaTestStack(t *testing.T, stack *victoria.Stack) {
	t.Helper()
	if stack == nil {
		return
	}
	t.Cleanup(func() {
		for _, pid := range stack.SubstrateRequest(0).PIDs {
			if pid <= 0 {
				continue
			}
			process, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			_ = process.Kill()
		}
	})
}

func killVictoriaTestComponent(t *testing.T, stack *victoria.Stack, name string) {
	t.Helper()
	pid := stack.SubstrateRequest(0).PIDs[name]
	if pid <= 0 {
		t.Fatalf("Victoria component %q has no managed process", name)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
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
		if netprobe.BindFree(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Victoria port %d did not become available", port)
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
