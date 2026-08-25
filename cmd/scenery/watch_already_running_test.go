package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
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

func TestFollowAlreadyRunningDevSessionOwnerExitInProcess(t *testing.T) {
	t.Parallel()

	runLogs := func(ctx context.Context, stdout io.Writer, args []string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ownerGone := func(context.Context, string) bool { return true }

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, false, "demo", "/tmp/app")
	if err := followAlreadyRunningDevSessionWith(context.Background(), console, "/tmp/app", runLogs, ownerGone); err != nil {
		t.Fatalf("followAlreadyRunningDevSessionWith = %v, want nil after owner exit", err)
	}
	if !strings.Contains(out.String(), "The running dev runtime stopped") {
		t.Fatalf("output missing runtime-stopped notice:\n%s", out.String())
	}
}
