package main

import (
	"context"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestRegisterDevSessionOnceWithFrontendBackendsInProcess(t *testing.T) {
	t.Parallel()

	request := localagent.RegisterRequest{
		BaseAppID: "demo", Environment: "local", AppRoot: "/repo/demo", SessionID: "main-demo", Status: "starting", OwnerPID: 42,
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "unix", Addr: "/state/run/api.sock"},
			"web":               {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
	}
	observed, registered := 0, 0
	session, err := registerDevSessionRequest(context.Background(), request, func(got localagent.RegisterRequest) {
		observed++
		if got.SessionID != request.SessionID || got.Backends["web"].Addr != "127.0.0.1:5173" {
			t.Fatalf("observed request = %+v", got)
		}
	}, func(_ context.Context, got localagent.RegisterRequest) (localagent.Session, error) {
		registered++
		return localagent.Session{SessionID: got.SessionID, Backends: got.Backends}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed != 1 || registered != 1 {
		t.Fatalf("register calls = observed:%d registered:%d, want one each", observed, registered)
	}
	if got := request.Backends[localagent.RouteAPI]; got.Network != "unix" || got.Addr == "" {
		t.Fatalf("api backend = %+v, want unix socket", got)
	}
	if got := request.Backends["web"]; got.Network != "tcp" || got.Addr != "127.0.0.1:5173" {
		t.Fatalf("web backend = %+v", got)
	}
	if session.SessionID != request.SessionID || session.Backends["web"].Addr != "127.0.0.1:5173" {
		t.Fatalf("registered session = %+v", session)
	}
}
