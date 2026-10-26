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

// worktreeRuntimeCall sends one development runtime RPC call to the verified
// owner of the app root's single running session and decodes its result. The
// session's app_id is added to params.
func worktreeRuntimeCall(ctx context.Context, appRoot, method string, params map[string]any, result any) error {
	start, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	root, _, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
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
		return fmt.Errorf("failed_precondition: %s requires one running scenery up session for %s", method, root)
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
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return err
		}
	}
	params["app_id"] = sessions[0].SessionID
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := conn.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: "cli-call", Method: method, Params: encoded}); err != nil {
		return err
	}
	for {
		var answer struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := conn.ReadJSON(&answer); err != nil {
			return err
		}
		if answer.ID != "cli-call" {
			continue
		}
		if answer.Error != nil {
			return fmt.Errorf("failed_precondition: development runtime %s: %s", method, answer.Error.Message)
		}
		return json.Unmarshal(answer.Result, result)
	}
}

// waitForWorktreeSource asks the running session to wait until the source as
// it is now is served.
func waitForWorktreeSource(ctx context.Context, appRoot string, timeout time.Duration) (runtimeSourceWait, error) {
	if timeout <= 0 {
		timeout = defaultSourceWaitTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	var result runtimeSourceWait
	if err := worktreeRuntimeCall(ctx, appRoot, "wait", map[string]any{"timeout_ms": timeout.Milliseconds()}, &result); err != nil {
		return runtimeSourceWait{}, err
	}
	if want := newCLIPayloadIdentity(devRuntimeWaitKind); result.cliPayloadIdentity != want {
		return runtimeSourceWait{}, fmt.Errorf("failed_precondition: the running scenery up answered %s %s, not %s %s; restart it with this Scenery", result.Kind, result.SchemaRevision, want.Kind, want.SchemaRevision)
	}
	return result, nil
}

// runWorktreeSourceWait reports the scenery.dev-runtime.wait result of the app
// root's running session. Every outcome other than served exits 3.
func runWorktreeSourceWait(ctx context.Context, stdout io.Writer, opts statusOptions) error {
	result, err := waitForWorktreeSource(ctx, opts.AppRoot, opts.Timeout)
	if err != nil {
		return err
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
