package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestFollowAlreadyRunningDevSessionDetachesOnInterrupt(t *testing.T) {
	oldLogs := runSceneryLogsFunc
	t.Cleanup(func() { runSceneryLogsFunc = oldLogs })
	var gotArgs []string
	runSceneryLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		gotArgs = args
		<-ctx.Done()
		return ctx.Err()
	}

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", "/tmp/app")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := followAlreadyRunningDevSession(ctx, console, "/tmp/app"); err != nil {
		t.Fatalf("followAlreadyRunningDevSession = %v, want nil on interrupt", err)
	}
	want := []string{"--follow", "--app-root", "/tmp/app"}
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("follow args = %#v, want %#v", gotArgs, want)
	}
	if !strings.Contains(out.String(), "Ctrl+C detaches without stopping it") {
		t.Fatalf("output missing detach hint:\n%s", out.String())
	}
}

func TestFollowAlreadyRunningDevSessionReportsFollowFailure(t *testing.T) {
	oldLogs := runSceneryLogsFunc
	t.Cleanup(func() { runSceneryLogsFunc = oldLogs })
	runSceneryLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		return fmt.Errorf("VictoriaLogs is unavailable")
	}

	console := newRunConsole(&bytes.Buffer{}, &bytes.Buffer{}, false, false, "demo", "/tmp/app")
	err := followAlreadyRunningDevSession(context.Background(), console, "/tmp/app")
	if err == nil || !strings.Contains(err.Error(), "could not follow its logs") || !strings.Contains(err.Error(), "VictoriaLogs is unavailable") {
		t.Fatalf("followAlreadyRunningDevSession error = %v, want wrapped follow failure", err)
	}
}

func TestFollowAlreadyRunningDevSessionExitsWhenOwnerStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	oldLogs := runSceneryLogsFunc
	oldInterval := devSessionOwnerExitPollInterval
	t.Cleanup(func() {
		runSceneryLogsFunc = oldLogs
		devSessionOwnerExitPollInterval = oldInterval
	})
	devSessionOwnerExitPollInterval = 20 * time.Millisecond
	runSceneryLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		<-ctx.Done()
		return ctx.Err()
	}

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
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: localagent.SessionID(root, ""),
		Status:    "running",
		OwnerPID:  owner.Process.Pid,
		Owner:     localagent.CaptureOwner(owner.Process.Pid, "test"),
	}); err != nil {
		t.Fatalf("register live owner session: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = owner.Process.Kill()
		_, _ = owner.Process.Wait()
	}()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", root)
	done := make(chan error, 1)
	go func() {
		done <- followAlreadyRunningDevSession(ctx, console, root)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("followAlreadyRunningDevSession = %v, want nil after owner exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("followAlreadyRunningDevSession did not detach after the owner stopped")
	}
	if !strings.Contains(out.String(), "The running dev runtime stopped") {
		t.Fatalf("output missing runtime-stopped notice:\n%s", out.String())
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}
