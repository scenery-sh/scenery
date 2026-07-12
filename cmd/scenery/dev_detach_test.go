package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestDevArgsForDetachedChild(t *testing.T) {
	t.Parallel()

	got := devArgsForDetachedChild([]string{"--app-root", "relative/app", "--detach", "--wait=ready", "-o", "json"}, "/tmp/app")
	want := []string{"-o", "jsonl", "--app-root", "/tmp/app"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("devArgsForDetachedChild = %#v, want %#v", got, want)
	}
}

func TestParseDevArgsWaitModes(t *testing.T) {
	t.Parallel()

	opts, err := parseDevArgs([]string{"--detach"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Wait != detachedDevWaitReady {
		t.Fatalf("default wait = %q, want ready", opts.Wait)
	}
	opts, err = parseDevArgs([]string{"--detach", "--wait=registered"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Wait != detachedDevWaitRegistered {
		t.Fatalf("wait = %q, want registered", opts.Wait)
	}
	opts, err = parseDevArgs([]string{"--detach", "--wait", "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Wait != detachedDevWaitReady {
		t.Fatalf("wait = %q, want ready", opts.Wait)
	}
	if _, err := parseDevArgs([]string{"--detach", "--wait=fast"}); err == nil || !strings.Contains(err.Error(), "invalid --wait") {
		t.Fatalf("parse invalid wait error = %v, want invalid --wait", err)
	}
}

func TestDetachedDevWaitTimeoutsSeparateRegistrationFromReadiness(t *testing.T) {
	t.Parallel()
	if got := detachedDevWaitTimeout(detachedDevWaitRegistered); got != 30*time.Second {
		t.Fatalf("registered timeout = %s", got)
	}
	if got := detachedDevWaitTimeout(detachedDevWaitReady); got != 2*time.Minute {
		t.Fatalf("ready timeout = %s", got)
	}
}

func TestDetachedDevChildMode(t *testing.T) {
	t.Setenv(detachedDevChildEnv, "yes")
	if !detachedDevChildMode() {
		t.Fatal("expected detached child mode")
	}
	t.Setenv(detachedDevChildEnv, "0")
	if detachedDevChildMode() {
		t.Fatal("did not expect detached child mode")
	}
}

func TestDetachedDevLogPathIsStableAndSafe(t *testing.T) {
	t.Parallel()

	paths := localagent.Paths{AgentDir: "/tmp/scenery-agent"}
	when := time.Date(2026, 5, 27, 12, 34, 56, 0, time.UTC)
	got := detachedDevLogPath(paths, filepath.Join("/tmp", "My App"), when)
	if !strings.HasPrefix(got, "/tmp/scenery-agent/dev/my-app-20260527T123456Z-") || !strings.HasSuffix(got, ".log") {
		t.Fatalf("detachedDevLogPath = %q", got)
	}
}

func TestWaitForDetachedDevSessionFindsOwnerPID(t *testing.T) {
	oldInterval := detachedDevStartupInterval
	detachedDevStartupInterval = time.Millisecond
	t.Cleanup(func() { detachedDevStartupInterval = oldInterval })

	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	server, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	}()

	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	registered := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Millisecond)
		_, _ = client.Register(ctx, localagent.RegisterRequest{
			BaseAppID: "detachapp",
			AppRoot:   appRoot,
			OwnerPID:  4242,
			Status:    "starting",
		})
		close(registered)
	}()
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	defer waitCancel()
	session, err := waitForDetachedDevSession(waitCtx, client, appRoot, 4242, detachedDevWaitRegistered, nil)
	if err != nil {
		t.Fatalf("waitForDetachedDevSession: %v", err)
	}
	<-registered
	if session.OwnerPID != 4242 || session.AppRoot != appRoot {
		t.Fatalf("session = %+v", session)
	}
}

func TestWaitForDetachedDevSessionRegisteredModeReturnsBeforeReady(t *testing.T) {
	oldInterval := detachedDevStartupInterval
	detachedDevStartupInterval = time.Millisecond
	t.Cleanup(func() { detachedDevStartupInterval = oldInterval })

	session := localagent.Session{AppRoot: "/tmp/app", OwnerPID: 4242, Status: "starting"}
	got, err := waitForDetachedDevSessionWithLister(context.Background(), func(context.Context, string) ([]localagent.Session, error) {
		return []localagent.Session{session}, nil
	}, "/tmp/app", 4242, detachedDevWaitRegistered, nil)
	if err != nil {
		t.Fatalf("waitForDetachedDevSessionWithLister: %v", err)
	}
	if got.Status != "starting" {
		t.Fatalf("status = %q, want starting", got.Status)
	}
}

func TestWaitForDetachedDevSessionReadyModeWaitsForAPIAndFrontends(t *testing.T) {
	oldInterval := detachedDevStartupInterval
	detachedDevStartupInterval = time.Millisecond
	t.Cleanup(func() { detachedDevStartupInterval = oldInterval })
	oldProbe := detachedDevBackendAcceptsConnections
	detachedDevBackendAcceptsConnections = func(backend devBackend) bool {
		return strings.HasPrefix(backend.Addr, "ready-")
	}
	t.Cleanup(func() { detachedDevBackendAcceptsConnections = oldProbe })

	calls := 0
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := waitForDetachedDevSessionWithLister(waitCtx, func(context.Context, string) ([]localagent.Session, error) {
		calls++
		session := localagent.Session{
			AppRoot:  "/tmp/app",
			OwnerPID: 4242,
			Status:   "running",
			Backends: map[string]localagent.Backend{
				localagent.RouteAPI: {Network: "unix", Addr: "ready-api"},
			},
		}
		if calls > 1 {
			session.Backends["web"] = localagent.Backend{Network: "tcp", Addr: "ready-web"}
		}
		return []localagent.Session{session}, nil
	}, "/tmp/app", 4242, detachedDevWaitReady, []string{"web"})
	if err != nil {
		t.Fatalf("waitForDetachedDevSessionWithLister: %v", err)
	}
	if _, ok := session.Backends["web"]; !ok {
		t.Fatalf("session backends = %+v, want web backend", session.Backends)
	}
	if calls < 2 {
		t.Fatalf("calls = %d, want wait for frontend backend", calls)
	}
}

func TestWaitForDetachedDevSessionReadyTimeoutReportsState(t *testing.T) {
	oldInterval := detachedDevStartupInterval
	detachedDevStartupInterval = time.Millisecond
	t.Cleanup(func() { detachedDevStartupInterval = oldInterval })

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := waitForDetachedDevSessionWithLister(waitCtx, func(context.Context, string) ([]localagent.Session, error) {
		return []localagent.Session{{AppRoot: "/tmp/app", OwnerPID: 4242, Status: "starting"}}, nil
	}, "/tmp/app", 4242, detachedDevWaitReady, nil)
	if err == nil || !strings.Contains(err.Error(), "last state: registered; status=starting") {
		t.Fatalf("timeout error = %v, want last state", err)
	}
}

func TestRejectDetachedDuplicateDevSessionRejectsLiveOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatalf("start owner fixture: %v", err)
	}
	defer func() {
		_ = owner.Process.Kill()
		_ = owner.Wait()
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	sessionID := localagent.SessionID(root, "")
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: sessionID,
		Status:    "running",
		OwnerPID:  owner.Process.Pid,
		Owner:     localagent.CaptureOwner(owner.Process.Pid, "test"),
	}); err != nil {
		t.Fatalf("register live owner session: %v", err)
	}

	err = rejectDetachedDuplicateDevSession(ctx, client, root, devOptions{})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("rejectDetachedDuplicateDevSession error = %v, want already running", err)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestWriteDetachedDevResultJSON(t *testing.T) {
	t.Parallel()

	result := detachedDevResult{
		SchemaVersion: "scenery.dev.detach.v1",
		Wait:          detachedDevWaitReady,
		PID:           123,
		LogPath:       "/tmp/dev.log",
		AttachCommand: `scenery logs --follow --app-root "/tmp/app"`,
		DownCommand:   `scenery down --app-root "/tmp/app"`,
		Session: localagent.Session{
			SessionID: "app-abc",
			OwnerPID:  123,
			RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
				localagent.RouteAPI: {URL: "http://api.app-abc.demo.localhost:9440"},
			}},
		},
	}
	var buf bytes.Buffer
	if err := writeDetachedDevResult(&buf, true, result); err != nil {
		t.Fatalf("writeDetachedDevResult: %v", err)
	}
	var payload detachedDevResult
	if err := decodeCLIJSON(buf.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, buf.String())
	}
	if payload.SchemaVersion != result.SchemaVersion || payload.PID != 123 || payload.Session.SessionID != "app-abc" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestWriteDetachedDevResultTextSeparatesAliases(t *testing.T) {
	t.Parallel()

	result := detachedDevResult{
		Wait:          detachedDevWaitReady,
		PID:           123,
		LogPath:       "/tmp/dev.log",
		AttachCommand: `scenery logs --follow --app-root "/tmp/app"`,
		DownCommand:   `scenery down --app-root "/tmp/app"`,
		Session: localagent.Session{
			SessionID: "app-abc",
			AppRoot:   "/tmp/app",
			Status:    "starting",
			RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
				localagent.RouteAPI: {URL: "https://api.app-abc.demo.localhost/"},
			}},
			Aliases: map[string]string{
				localagent.RouteAPI: "https://api.demo.localhost/",
			},
			AliasConflicts: map[string]localagent.AliasLease{
				"web": {
					Host:    "demo.localhost",
					AppRoot: "/tmp/other-app",
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := writeDetachedDevResult(&buf, false, result); err != nil {
		t.Fatalf("writeDetachedDevResult: %v", err)
	}
	output := buf.String()
	for _, want := range []string{
		"[+] Running 1/1\n - App /tmp/app  Starting  pid=123",
		"Use:\n  status  scenery ps --app-root \"/tmp/app\"\n  logs    scenery logs --follow --app-root \"/tmp/app\"\n  stop    scenery down --app-root \"/tmp/app\"",
		"Log file: /tmp/dev.log",
		"Routes currently registered:\n  api        https://api.app-abc.demo.localhost/",
		"Aliases currently claimed:\n  api        https://api.demo.localhost/",
		"Aliases held by other app roots:\n  web        demo.localhost owned by /tmp/other-app",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestWriteDetachedDevResultTextIncludesReadyBanner(t *testing.T) {
	t.Parallel()

	result := detachedDevResult{
		Wait:          detachedDevWaitReady,
		PID:           123,
		LogPath:       "/tmp/dev.log",
		AttachCommand: `scenery logs --follow --app-root "/tmp/app"`,
		DownCommand:   `scenery down --app-root "/tmp/app"`,
		Session: localagent.Session{
			AppRoot: "/tmp/app",
			Status:  "running",
			RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
				localagent.RouteAPI:       {URL: "https://app.localhost/api/"},
				localagent.RouteDashboard: {URL: "https://app.localhost/consolenext/"},
				"web":                     {URL: "https://app.localhost/web/"},
			}},
		},
	}
	var buf bytes.Buffer
	if err := writeDetachedDevResult(&buf, false, result); err != nil {
		t.Fatalf("writeDetachedDevResult: %v", err)
	}
	output := buf.String()
	for _, want := range []string{
		"scenery development server running",
		"API:",
		"https://app.localhost/api/",
		"Dashboard:",
		"https://app.localhost/consolenext/",
		"Frontend web:",
		"https://app.localhost/web/",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
