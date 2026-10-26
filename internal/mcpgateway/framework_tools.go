package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scenery.sh/errs"
	"scenery.sh/internal/mcpcontract"
)

// Framework tools read, cancel and wait for durable executions the calling
// principal accepted.

func (g *gateway) addFrameworkTools(server *mcp.Server) {
	input := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"execution_id": map[string]any{"type": "string"}},
		"required":             []any{"execution_id"},
		"additionalProperties": false,
	}
	waitInput := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"execution_id": map[string]any{"type": "string"},
			"timeout_ms":   map[string]any{"type": "integer", "minimum": 0, "maximum": maxDurableWait.Milliseconds(), "description": "How long to wait; default 30000. A timeout ends only the wait."},
		},
		"required":             []any{"execution_id"},
		"additionalProperties": false,
	}
	server.AddTool(&mcp.Tool{Name: mcpcontract.StatusToolName, Description: "Read durable execution status", InputSchema: input, OutputSchema: frameworkOutputSchema(executionStatusSchema())}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.frameworkCall(ctx, req, durableStatus)
	})
	server.AddTool(&mcp.Tool{Name: mcpcontract.CancelToolName, Description: "Cancel durable execution", InputSchema: input, OutputSchema: frameworkOutputSchema(executionCancelSchema())}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.frameworkCall(ctx, req, durableCancel)
	})
	server.AddTool(&mcp.Tool{Name: mcpcontract.WaitToolName, Description: "Wait until a durable execution reaches a final state, up to a bounded timeout", InputSchema: waitInput, OutputSchema: frameworkOutputSchema(executionWaitSchema())}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.frameworkCall(ctx, req, durableWait)
	})
}

type durableOperation int

const (
	durableStatus durableOperation = iota
	durableCancel
	durableWait
)

const (
	defaultDurableWait = 30 * time.Second
	maxDurableWait     = 120 * time.Second
)

// durableWaitPoll is how often a wait re-reads execution status.
var durableWaitPoll = 200 * time.Millisecond

func (g *gateway) frameworkCall(ctx context.Context, req *mcp.CallToolRequest, operation durableOperation) (*mcp.CallToolResult, error) {
	if req == nil || req.Params == nil {
		return nil, errors.New("missing execution parameters")
	}
	var input struct {
		ExecutionID string `json:"execution_id"`
		TimeoutMS   *int64 `json:"timeout_ms"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil || input.ExecutionID == "" {
		return errorToolResult(&errs.Error{Code: errs.InvalidArgument, Message: "execution_id is required"}), nil
	}
	timeout := defaultDurableWait
	if input.TimeoutMS != nil {
		if operation != durableWait || *input.TimeoutMS < 0 || *input.TimeoutMS > maxDurableWait.Milliseconds() {
			return errorToolResult(&errs.Error{Code: errs.InvalidArgument, Message: fmt.Sprintf("timeout_ms must be between 0 and %d for %s", maxDurableWait.Milliseconds(), mcpcontract.WaitToolName)}), nil
		}
		timeout = time.Duration(*input.TimeoutMS) * time.Millisecond
	}
	callCtx, ok := ctx.Value(assertionContextKey).(mcpcontract.ToolCallContext)
	if !ok {
		return nil, ErrUnauthorized
	}
	callCtx.RequestID = newRequestID()
	var (
		value json.RawMessage
		err   error
	)
	switch operation {
	case durableCancel:
		value, err = g.cfg.Durable.Cancel(ctx, callCtx, input.ExecutionID)
	case durableWait:
		value, err = g.waitDurable(ctx, callCtx, input.ExecutionID, timeout)
	default:
		value, err = g.cfg.Durable.Status(ctx, callCtx, input.ExecutionID)
	}
	if err != nil {
		return errorToolResult(err), nil
	}
	result, encoded := outcomeResult(mcpcontract.ToolOutcome{Outcome: "completed", Value: value})
	if int64(len(encoded)) > defaultMaxBodyBytes {
		return errorToolResult(&errs.Error{Code: errs.ResourceExhausted, Message: "durable result exceeds configured limit"}), nil
	}
	return result, nil
}

// waitDurable re-reads an owned execution's status until it is final or the
// timeout elapses. A timeout ends only this wait; it never claims that the
// execution finished or was canceled.
func (g *gateway) waitDurable(ctx context.Context, callCtx mcpcontract.ToolCallContext, executionID string, timeout time.Duration) (json.RawMessage, error) {
	started := time.Now()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last json.RawMessage
	finish := func(final bool) (json.RawMessage, error) {
		return json.Marshal(mcpcontract.ExecutionWait{Status: last, Final: final, TimedOut: !final, WaitedMS: time.Since(started).Milliseconds()})
	}
	for {
		status, err := g.cfg.Durable.Status(waitCtx, callCtx, executionID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if waitCtx.Err() != nil {
				if len(last) > 0 {
					return finish(false)
				}
				return nil, waitCtx.Err()
			}
			return nil, err
		}
		var current mcpcontract.ExecutionStatus
		if err := json.Unmarshal(status, &current); err != nil {
			return nil, err
		}
		last = status
		if mcpcontract.TerminalExecutionState(current.State) {
			return finish(true)
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return finish(false)
		case <-time.After(durableWaitPoll):
		}
	}

}

func frameworkOutputSchema(value map[string]any) map[string]any {
	return map[string]any{"type": "object", "oneOf": []any{
		map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"outcome": map[string]any{"type": "string", "const": "completed"}, "value": value},
			"required":   []any{"outcome", "value"},
		},
		map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"outcome": map[string]any{"type": "string", "const": mcpcontract.FailedOutcome},
				"problem": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}},
					"required":   []any{"code", "message"},
				},
			},
			"required": []any{"outcome", "problem"},
		},
	}}
}

func executionStatusSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"execution_id": map[string]any{"type": "string"},
			"state":        map[string]any{"type": "string", "description": "queued, running, succeeded, failed, canceled or skipped"},
			"outcome":      map[string]any{"description": "The execution's result once it succeeded, or {error} once it failed"},
		},
		"required": []any{"execution_id", "state"},
	}
}

func executionCancelSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"execution_id": map[string]any{"type": "string"},
			"state":        map[string]any{"type": "string", "const": "canceled"},
		},
		"required": []any{"execution_id", "state"},
	}
}

func executionWaitSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"status":    executionStatusSchema(),
			"final":     map[string]any{"type": "boolean", "description": "The execution reached a final state"},
			"timed_out": map[string]any{"type": "boolean", "description": "The wait ended before a final state; the execution continues"},
			"waited_ms": map[string]any{"type": "integer", "minimum": 0},
		},
		"required": []any{"status", "final", "timed_out", "waited_ms"},
	}
}
