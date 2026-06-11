package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

type fakeSubstrateAdapter struct {
	kind       string
	starts     int
	startErr   error
	probeOK    bool
	startDone  chan error
	startPID   int
	reuseCount int
}

type fakeSubstrateHandle struct {
	kind      string
	status    string
	pid       int
	done      chan error
	startedAt time.Time
}

func (a *fakeSubstrateAdapter) Kind() string       { return a.kind }
func (a *fakeSubstrateAdapter) SourceID() string   { return a.kind }
func (a *fakeSubstrateAdapter) SourceName() string { return a.kind }
func (a *fakeSubstrateAdapter) Role() string       { return "test" }
func (a *fakeSubstrateAdapter) Start(context.Context, string) (managedSubstrateHandle, error) {
	a.starts++
	if a.startErr != nil {
		return nil, a.startErr
	}
	done := a.startDone
	if done == nil {
		done = make(chan error)
	}
	return &fakeSubstrateHandle{kind: a.kind, status: "ready", pid: firstPositiveInt(a.startPID, os.Getpid()), done: done, startedAt: time.Now().UTC()}, nil
}
func (a *fakeSubstrateAdapter) FromSubstrate(_ context.Context, substrate localagent.Substrate) (managedSubstrateHandle, bool) {
	a.reuseCount++
	if !a.probeOK {
		return nil, false
	}
	return &fakeSubstrateHandle{kind: a.kind, status: substrate.Status, pid: firstPositiveInt(substrate.OwnerPID, substrate.Owner.PID), startedAt: substrate.CreatedAt}, true
}
func (a *fakeSubstrateAdapter) ReadyFields(managedSubstrateHandle) map[string]any {
	return map[string]any{"fresh": true}
}
func (a *fakeSubstrateAdapter) ReuseFields(managedSubstrateHandle, localagent.Substrate) map[string]any {
	return map[string]any{"reused": true}
}
func (a *fakeSubstrateAdapter) ExitStatus(managedSubstrateComponent) string  { return "degraded" }
func (a *fakeSubstrateAdapter) ExitMessage(managedSubstrateComponent) string { return "fake exited" }
func (a *fakeSubstrateAdapter) EventSource(_ managedSubstrateHandle, component managedSubstrateComponent, status string) devdash.DevSource {
	return devdash.DevSource{ID: a.kind + "." + component.Name, Kind: "substrate", Name: component.DisplayName, Role: "test", Status: status}
}

func (h *fakeSubstrateHandle) SubstrateRequest(ownerPID int) localagent.UpsertSubstrateRequest {
	if h.pid > 0 {
		ownerPID = h.pid
	}
	return localagent.UpsertSubstrateRequest{
		Kind:     h.kind,
		Status:   firstNonEmpty(h.status, "ready"),
		OwnerPID: ownerPID,
		PIDs:     map[string]int{"server": ownerPID},
		URLs:     map[string]string{"web": "http://127.0.0.1:1"},
	}
}
func (h *fakeSubstrateHandle) MarkExternal() {}
func (h *fakeSubstrateHandle) Components() []managedSubstrateComponent {
	if h.done == nil {
		return nil
	}
	return []managedSubstrateComponent{{
		Name:        "server",
		DisplayName: "Fake",
		Role:        "test",
		URL:         "http://127.0.0.1:1",
		Done:        h.done,
		ExitRecord: func(err error) localagent.SubstrateExit {
			return substrateExitRecord("server", h.pid, h.startedAt, "", "/tmp/fake.stderr.log", err, nil)
		},
	}}
}

func TestManagedSubstrateManagerReusesVerifiedSubstrateWithoutStarting(t *testing.T) {
	t.Parallel()

	ctx, client := startManagedSubstrateManagerTestAgent(t)
	ownerPID := startFakeSubstrateOwner(t)
	created, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     "fake",
		Status:   "ready",
		OwnerPID: ownerPID,
		PIDs:     map[string]int{"server": ownerPID},
		URLs:     map[string]string{"web": "http://127.0.0.1:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSubstrateAdapter{kind: "fake", probeOK: true}
	handle, reused, err := (managedSubstrateManager{agent: client}).Ensure(ctx, t.TempDir(), adapter)
	if err != nil {
		t.Fatal(err)
	}
	if handle == nil || !reused || adapter.starts != 0 || adapter.reuseCount != 1 {
		t.Fatalf("reuse result handle=%T reused=%v starts=%d reuseCount=%d", handle, reused, adapter.starts, adapter.reuseCount)
	}
	after, err := client.GetSubstrate(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at changed on reuse: before=%s after=%s", created.CreatedAt, after.CreatedAt)
	}
}

func TestManagedSubstrateManagerDeletesStaleOwnerAndStartsFresh(t *testing.T) {
	t.Parallel()

	ctx, client := startManagedSubstrateManagerTestAgent(t)
	ownerPID := startFakeSubstrateOwner(t)
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     "fake",
		Status:   "ready",
		OwnerPID: 99999991,
		URLs:     map[string]string{"web": "http://127.0.0.1:1"},
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSubstrateAdapter{kind: "fake", probeOK: true, startPID: ownerPID}
	handle, reused, err := (managedSubstrateManager{agent: client}).Ensure(ctx, t.TempDir(), adapter)
	if err != nil {
		t.Fatal(err)
	}
	if handle == nil || reused || adapter.starts != 1 {
		t.Fatalf("fresh result handle=%T reused=%v starts=%d", handle, reused, adapter.starts)
	}
	substrate, err := client.GetSubstrate(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if substrate.OwnerPID != ownerPID {
		t.Fatalf("fresh owner pid = %d, want %d", substrate.OwnerPID, ownerPID)
	}
}

func TestManagedSubstrateManagerDeletesFailedMaterializationAndProbe(t *testing.T) {
	t.Parallel()

	ctx, client := startManagedSubstrateManagerTestAgent(t)
	ownerPID := startFakeSubstrateOwner(t)
	for _, probeOK := range []bool{false} {
		if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
			Kind:     "fake",
			Status:   "ready",
			OwnerPID: ownerPID,
			PIDs:     map[string]int{"server": ownerPID},
			URLs:     map[string]string{"web": "http://127.0.0.1:1"},
		}); err != nil {
			t.Fatal(err)
		}
		adapter := &fakeSubstrateAdapter{kind: "fake", probeOK: probeOK, startPID: ownerPID}
		if _, _, err := (managedSubstrateManager{agent: client}).Ensure(ctx, t.TempDir(), adapter); err != nil {
			t.Fatal(err)
		}
		if adapter.starts != 1 {
			t.Fatalf("starts = %d, want fresh start after failed materialization/probe", adapter.starts)
		}
	}
}

func TestManagedSubstrateManagerStartupErrorDoesNotUpsert(t *testing.T) {
	t.Parallel()

	ctx, client := startManagedSubstrateManagerTestAgent(t)
	adapter := &fakeSubstrateAdapter{kind: "fake", startErr: errors.New("exited before ready")}
	handle, reused, err := (managedSubstrateManager{agent: client}).Ensure(ctx, t.TempDir(), adapter)
	if err == nil || !errors.Is(err, adapter.startErr) || handle != nil || reused {
		t.Fatalf("startup result handle=%T reused=%v err=%v", handle, reused, err)
	}
	if _, err := client.GetSubstrate(ctx, "fake"); !localagent.IsNotFound(err) {
		t.Fatalf("substrate after failed start err=%v", err)
	}
}

func TestManagedSubstrateManagerMonitorRecordsExitState(t *testing.T) {
	t.Parallel()

	ctx, client := startManagedSubstrateManagerTestAgent(t)
	done := make(chan error, 1)
	ownerPID := startFakeSubstrateOwner(t)
	handle := &fakeSubstrateHandle{kind: "fake", status: "ready", pid: ownerPID, done: done, startedAt: time.Now().Add(-time.Second).UTC()}
	if _, err := client.UpsertSubstrate(ctx, handle.SubstrateRequest(ownerPID)); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSubstrateAdapter{kind: "fake", probeOK: true}
	monitorDone := (managedSubstrateManager{agent: client}).Monitor(handle, adapter)
	done <- fmt.Errorf("exit status 7")
	close(done)
	substrate := waitForSubstrateStatus(t, ctx, client, "fake", "degraded")
	if substrate.LastExit == nil || substrate.ComponentExits["server"].Component != "server" {
		t.Fatalf("exit substrate = %+v", substrate)
	}
	waitForMonitorDone(t, monitorDone)
}

func startManagedSubstrateManagerTestAgent(t *testing.T) (context.Context, *localagent.Client) {
	t.Helper()
	ctx := context.Background()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(runCtx) }()
	t.Cleanup(func() {
		stopAgentServerForTest(t, cancel, done)
	})
	client := localagent.NewClient(server.Paths().SocketPath)
	t.Cleanup(client.CloseIdleConnections)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	return ctx, client
}

func startFakeSubstrateOwner(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake substrate owner: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}
