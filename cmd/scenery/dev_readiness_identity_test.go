package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func stubDetachedReadyOwners(t *testing.T) {
	t.Helper()
	previous := detachedDevVerifyOwner
	detachedDevVerifyOwner = func(localagent.Owner) error { return nil }
	t.Cleanup(func() { detachedDevVerifyOwner = previous })
}

func TestDetachedReadyWaitsForPublishedAPIIdentity(t *testing.T) {
	stubDetachedReadyOwners(t)
	interval, backend, routes := detachedDevStartupInterval, detachedDevBackendAcceptsConnections, detachedDevRoutesReachable
	detachedDevStartupInterval = time.Millisecond
	detachedDevBackendAcceptsConnections = func(devBackend) bool { return true }
	detachedDevRoutesReachable = func(context.Context, localagent.Session) error { return nil }
	t.Cleanup(func() {
		detachedDevStartupInterval, detachedDevBackendAcceptsConnections, detachedDevRoutesReachable = interval, backend, routes
	})
	calls := 0
	session := localagent.Session{
		AppRoot: "/tmp/app", OwnerPID: 42, Status: "running",
		Owner:    localagent.Owner{PID: 42, StartedAt: "owner"},
		Backends: map[string]localagent.Backend{localagent.RouteAPI: {Network: "unix", Addr: "listening"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := waitForDetachedDevSessionWithLister(ctx, func(context.Context, string) ([]localagent.Session, error) {
		calls++
		if calls == 2 {
			session.AppPID = "43"
		}
		if calls == 3 {
			session.Processes = map[string]localagent.Process{localagent.RouteAPI: {PID: 43, Owner: localagent.Owner{PID: 43, StartedAt: "api"}}}
		}
		return []localagent.Session{session}, nil
	}, session.AppRoot, session.OwnerPID, detachedDevWaitReady, nil)
	if err != nil || calls != 3 || got.AppPID != "43" || got.Processes[localagent.RouteAPI].Owner.StartedAt == "" {
		t.Fatalf("ready returned before complete publication: calls=%d session=%+v err=%v", calls, got, err)
	}
}

func TestDetachedReadyRejectsPermanentControlErrorImmediately(t *testing.T) {
	want := errors.New("incompatible specification")
	calls := 0
	_, err := waitForDetachedDevSessionWithLister(context.Background(), func(context.Context, string) ([]localagent.Session, error) {
		calls++
		return nil, want
	}, "/tmp/app", 42, detachedDevWaitReady, nil)
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("permanent error retried: calls=%d err=%v", calls, err)
	}
}

func TestDetachedReadyRejectsChangedProcessOwner(t *testing.T) {
	t.Parallel()
	session := localagent.Session{OwnerPID: 42, Owner: localagent.Owner{PID: 42, StartedAt: "owner"}, AppPID: "43",
		Processes: map[string]localagent.Process{localagent.RouteAPI: {PID: 43, Owner: localagent.Owner{PID: 43, StartedAt: "api"}}}}
	state, err := detachedDevPublishedIdentity(session, func(owner localagent.Owner) error {
		if owner.PID == 43 {
			return errors.New("start time changed")
		}
		return nil
	})
	if state != "" || err == nil || !strings.Contains(err.Error(), "start time changed") {
		t.Fatalf("contradictory ownership treated as pending: state=%q err=%v", state, err)
	}
	var diagnostic *cliDiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.code != 3 || diagnostic.diagnostic.Code != "SCN8003" {
		t.Fatalf("ownership mismatch lost its public precondition classification: %v", err)
	}
}
