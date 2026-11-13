package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	localagent "scenery.sh/internal/agent"
)

func readWorktreeRuntimeStatus(ctx context.Context, root string, session localagent.Session) (runtimeStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := commandWorktreeClient(ctx, root)
	if err != nil {
		return runtimeStatus{}, err
	}
	defer client.CloseIdleConnections()
	health, err := client.Health(ctx)
	if err != nil {
		return runtimeStatus{}, err
	}
	if health.DashboardBackend.Network != "tcp" || health.DashboardBackend.Addr == "" {
		return runtimeStatus{}, fmt.Errorf("verified worktree runtime status is unavailable")
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+health.DashboardBackend.Addr+"/__scenery", nil)
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		return runtimeStatus{}, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return runtimeStatus{}, err
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return runtimeStatus{}, err
	}
	params, err := json.Marshal(map[string]string{"app_id": session.SessionID})
	if err != nil {
		return runtimeStatus{}, err
	}
	if err := conn.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: "ps-source", Method: "status", Params: params}); err != nil {
		return runtimeStatus{}, err
	}
	for {
		var response struct {
			ID     string        `json:"id"`
			Result runtimeStatus `json:"result"`
			Error  *rpcError     `json:"error"`
		}
		if err := conn.ReadJSON(&response); err != nil {
			return runtimeStatus{}, err
		}
		if response.ID != "ps-source" {
			continue
		}
		if response.Error != nil {
			return runtimeStatus{}, fmt.Errorf("worktree runtime status: %s", response.Error.Message)
		}
		status := response.Result
		want := newCLIPayloadIdentity("scenery.dev-runtime.status")
		if status.Kind != want.Kind || status.SchemaRevision != want.SchemaRevision || status.SessionID != session.SessionID || status.AppRoot != root || status.PID != session.AppPID {
			return runtimeStatus{}, fmt.Errorf("worktree runtime status identity does not match the verified session: kind=%s schema=%s session=%s root=%s pid=%s (want session=%s root=%s pid=%s)", status.Kind, status.SchemaRevision, status.SessionID, status.AppRoot, status.PID, session.SessionID, root, session.AppPID)
		}
		return status, nil
	}
}
