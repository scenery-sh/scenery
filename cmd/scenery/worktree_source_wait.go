package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gorilla/websocket"
	"scenery.sh/internal/app"
)

// runWorktreeSourceWait asks the verified owner of the app root's running
// session to wait until the source as it is now is served, and reports the
// scenery.dev-runtime.wait result. Every outcome other than served exits 3.
func runWorktreeSourceWait(ctx context.Context, stdout io.Writer, opts statusOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, _, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultSourceWaitTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
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
		return fmt.Errorf("failed_precondition: waiting for the served source requires one running scenery up session for %s", root)
	}
	backend := health.DashboardBackend
	if backend.Network != "tcp" || backend.Addr == "" {
		return fmt.Errorf("failed_precondition: the worktree runtime control backend is unavailable")
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
	params, err := json.Marshal(map[string]any{"app_id": sessions[0].SessionID, "timeout_ms": timeout.Milliseconds()})
	if err != nil {
		return err
	}
	if err := conn.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: "source-wait", Method: "wait", Params: params}); err != nil {
		return err
	}
	for {
		var answer struct {
			ID     string            `json:"id"`
			Result runtimeSourceWait `json:"result"`
			Error  *rpcError         `json:"error"`
		}
		if err := conn.ReadJSON(&answer); err != nil {
			return err
		}
		if answer.ID != "source-wait" {
			continue
		}
		if answer.Error != nil {
			return fmt.Errorf("failed_precondition: worktree source wait: %s", answer.Error.Message)
		}
		result := answer.Result
		if want := newCLIPayloadIdentity(devRuntimeWaitKind); result.cliPayloadIdentity != want {
			return fmt.Errorf("failed_precondition: the running scenery up answered %s %s, not %s %s; restart it with this Scenery", result.Kind, result.SchemaRevision, want.Kind, want.SchemaRevision)
		}
		if opts.JSON {
			if err := writeCLIJSON(stdout, result); err != nil {
				return err
			}
		} else {
			generation := uint64(0)
			if result.Serving != nil {
				generation = result.Serving.Generation
			}
			_, _ = fmt.Fprintf(stdout, "%s after %dms (target %s, generation %d, freshness %s)\n", result.Outcome, result.WaitedMS, result.TargetSourceSnapshotDigest, generation, result.SourceFreshness)
			if result.CompileError != "" {
				_, _ = fmt.Fprintln(stdout, result.CompileError)
			}
		}
		if !result.OK {
			return &silentCLIError{err: fmt.Errorf("source wait ended %s", result.Outcome), code: 3}
		}
		return nil
	}
}
