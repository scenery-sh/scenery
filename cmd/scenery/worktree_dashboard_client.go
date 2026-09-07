package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// Route a CLI dashboard action to the verified live worktree owner, never a
// process-local default observability stack or the machine dashboard.
func clearWorktreeTraces(ctx context.Context, root, _ string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := commandWorktreeClient(ctx, root)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	health, err := client.Health(ctx)
	if err != nil {
		return err
	}
	sessions, err := client.List(ctx, root)
	if err != nil {
		return err
	}
	if len(sessions) != 1 || sessions[0].AppRoot != root {
		return fmt.Errorf("trace clear requires one current worktree session")
	}
	backend := health.DashboardBackend
	if backend.Network != "tcp" || backend.Addr == "" {
		return fmt.Errorf("worktree dashboard is unavailable")
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+backend.Addr+"/__scenery", nil)
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	params, err := json.Marshal(map[string]string{"app_id": sessions[0].SessionID})
	if err != nil {
		return err
	}
	if err := conn.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: "clear-traces", Method: "traces/clear", Params: params}); err != nil {
		return err
	}
	for {
		var response rpcResponse
		if err := conn.ReadJSON(&response); err != nil {
			return err
		}
		if id, ok := response.ID.(string); !ok || id != "clear-traces" {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("worktree trace clear: %s", response.Error.Message)
		}
		return nil
	}
}
