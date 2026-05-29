package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
)

func TestDevArgsForDetachedChild(t *testing.T) {
	t.Parallel()

	got := devArgsForDetachedChild([]string{"--app-root", "relative/app", "--detach", "--json"}, "/tmp/app")
	want := []string{"--json", "--app-root", "/tmp/app"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("devArgsForDetachedChild = %#v, want %#v", got, want)
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

	paths := localagent.Paths{AgentDir: "/tmp/onlava-agent"}
	when := time.Date(2026, 5, 27, 12, 34, 56, 0, time.UTC)
	got := detachedDevLogPath(paths, filepath.Join("/tmp", "My App"), when)
	if !strings.HasPrefix(got, "/tmp/onlava-agent/dev/my-app-20260527T123456Z-") || !strings.HasSuffix(got, ".log") {
		t.Fatalf("detachedDevLogPath = %q", got)
	}
}

func TestWaitForDetachedDevSessionFindsOwnerPID(t *testing.T) {
	oldInterval := detachedDevStartupInterval
	detachedDevStartupInterval = time.Millisecond
	t.Cleanup(func() { detachedDevStartupInterval = oldInterval })

	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
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
	session, err := waitForDetachedDevSession(waitCtx, client, appRoot, 4242)
	if err != nil {
		t.Fatalf("waitForDetachedDevSession: %v", err)
	}
	<-registered
	if session.OwnerPID != 4242 || session.AppRoot != appRoot {
		t.Fatalf("session = %+v", session)
	}
}

func TestWriteDetachedDevResultJSON(t *testing.T) {
	t.Parallel()

	result := detachedDevResult{
		SchemaVersion: "onlava.dev.detach.v1",
		PID:           123,
		LogPath:       "/tmp/dev.log",
		AttachCommand: `onlava attach --app-root "/tmp/app" --session app-abc`,
		DownCommand:   "onlava down --session app-abc",
		Session: localagent.Session{
			SessionID: "app-abc",
			OwnerPID:  123,
			Routes: map[string]string{
				localagent.RouteAPI: "http://api.app-abc.onlava.localhost:9440",
			},
		},
	}
	var buf bytes.Buffer
	if err := writeDetachedDevResult(&buf, true, result); err != nil {
		t.Fatalf("writeDetachedDevResult: %v", err)
	}
	var payload detachedDevResult
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, buf.String())
	}
	if payload.SchemaVersion != result.SchemaVersion || payload.PID != 123 || payload.Session.SessionID != "app-abc" {
		t.Fatalf("payload = %+v", payload)
	}
}
