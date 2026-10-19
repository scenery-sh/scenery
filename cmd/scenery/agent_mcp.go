package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scenery.sh/errs"
	"scenery.sh/internal/compiler"
)

// The development MCP adapter exposes the running worktree runtime to MCP
// clients: status, waiting for the served source, the application's CLI
// bindings and their live invocation, compile diagnostics and trace details.
// Every tool calls the same handlers as the CLI and the development runtime
// RPC; `scenery up` keeps owning the runtime lifecycle.

func newDevelopmentMCPServer(root string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "scenery-development", Title: "Scenery development runtime", Version: cliProducer().Version}, nil)
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	add := func(tool *mcp.Tool, handle func(context.Context, json.RawMessage) (any, error)) {
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			arguments := json.RawMessage(`{}`)
			if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
				arguments = req.Params.Arguments
			}
			value, err := handle(ctx, arguments)
			if err != nil {
				return developmentMCPFailure(err), nil
			}
			return developmentMCPResult(value)
		})
	}
	add(&mcp.Tool{Name: "runtime_status", Description: "Read the running scenery up session's status: serving generation and build identity, source freshness, build blocks and service processes.", InputSchema: object(map[string]any{}), Annotations: readOnly}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := decodeDevelopmentMCPArguments(raw, &struct{}{}); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var status runtimeStatus
		return &status, worktreeRuntimeCall(ctx, root, "status", map[string]any{}, &status)
	})
	add(&mcp.Tool{Name: "runtime_wait", Description: "Wait until the running scenery up serves the source as it is now; the outcome says whether it was served or why not.", InputSchema: object(map[string]any{
		"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": maxSourceWaitTimeout.Milliseconds(), "description": "How long to wait; default 30000."},
	}), Annotations: readOnly}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			TimeoutMS int64 `json:"timeout_ms"`
		}
		if err := decodeDevelopmentMCPArguments(raw, &input); err != nil {
			return nil, err
		}
		return waitForWorktreeSource(ctx, root, time.Duration(input.TimeoutMS)*time.Millisecond)
	})
	add(&mcp.Tool{Name: "bindings_list", Description: "List the application's CLI bindings with their typed arguments, flags and outcome map.", InputSchema: object(map[string]any{}), Annotations: readOnly}, func(_ context.Context, raw json.RawMessage) (any, error) {
		if err := decodeDevelopmentMCPArguments(raw, &struct{}{}); err != nil {
			return nil, err
		}
		_, _, result, bindings, err := loadCLIBindingsAt(root)
		if err != nil {
			return nil, err
		}
		commands := make([]helpCommandEntry, 0, len(bindings))
		for _, binding := range bindings {
			commands = append(commands, bindingHelpEntry(result.Manifest.Resources, binding))
		}
		return map[string]any{"bindings": commands, "contract_revision": result.Manifest.ContractRevision}, nil
	})
	add(&mcp.Tool{Name: "binding_invoke", Description: "Wait until the running scenery up serves the current source, then run one application CLI binding in its serving generation. Arguments use the binding's own CLI grammar.", InputSchema: object(map[string]any{
		"command":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "description": "The binding's command words, as bindings_list reports them."},
		"arguments": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The binding's positional arguments and flags."},
	}, "command"), Annotations: &mcp.ToolAnnotations{OpenWorldHint: new(false)}}, func(_ context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Command   []string `json:"command"`
			Arguments []string `json:"arguments"`
		}
		if err := decodeDevelopmentMCPArguments(raw, &input); err != nil {
			return nil, err
		}
		appRoot, cfg, result, bindings, err := loadCLIBindingsAt(root)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			cli, _ := binding.Spec["cli"].(map[string]any)
			if slices.Equal(contractStrings(cli["command"]), input.Command) {
				return executeBindingCLI(appRoot, cfg, result, binding, input.Arguments, true)
			}
		}
		return nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("no CLI binding has the command %q", strings.Join(input.Command, " "))}
	})
	add(&mcp.Tool{Name: "diagnostics", Description: "Compile the application and report its contract diagnostics with workspace and contract revisions.", InputSchema: object(map[string]any{}), Annotations: readOnly}, func(_ context.Context, raw json.RawMessage) (any, error) {
		if err := decodeDevelopmentMCPArguments(raw, &struct{}{}); err != nil {
			return nil, err
		}
		result, err := compiler.Compile(root)
		if err != nil {
			return nil, err
		}
		contractRevision := any(nil)
		if result.Manifest != nil {
			contractRevision = result.Manifest.ContractRevision
		}
		return map[string]any{"valid": result.Valid(), "diagnostics": result.Diagnostics, "workspace_revision": result.WorkspaceRevision, "contract_revision": contractRevision}, nil
	})
	add(&mcp.Tool{Name: "trace_get", Description: "Read one trace of the running session: chronological spans with their events, including normalized SQL.", InputSchema: object(map[string]any{
		"trace_id": map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"},
	}, "trace_id"), Annotations: readOnly}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var input struct {
			TraceID string `json:"trace_id"`
		}
		if err := decodeDevelopmentMCPArguments(raw, &input); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		var detail json.RawMessage
		return detail, worktreeRuntimeCall(ctx, root, "traces/get", map[string]any{"trace_id": input.TraceID}, &detail)
	})
	return server
}

func decodeDevelopmentMCPArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &errs.Error{Code: errs.InvalidArgument, Message: "invalid tool arguments: " + err.Error()}
	}
	return nil
}

func developmentMCPResult(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return developmentMCPFailure(err), nil
	}
	var structured any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return developmentMCPFailure(err), nil
	}
	if _, ok := structured.(map[string]any); !ok {
		structured = map[string]any{"value": structured}
		encoded, _ = json.Marshal(structured)
	}
	return &mcp.CallToolResult{StructuredContent: structured, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
}

// developmentMCPFailure reports a stable problem code with the message, so a
// client can tell a bad argument from a missing runtime.
func developmentMCPFailure(err error) *mcp.CallToolResult {
	code := string(errs.Code(err))
	if code == "" || code == string(errs.Unknown) {
		code = "internal_error"
		if prefix, _, found := strings.Cut(err.Error(), ":"); found && slices.Contains([]string{"invalid_request", "failed_precondition", "not_found", "permission_denied", "capability_unavailable", "unavailable", "invalid_argument"}, prefix) {
			code = prefix
		}
		var coded *codedCLIError
		if errors.As(err, &coded) && code == "internal_error" {
			code = "failed_precondition"
		}
	}
	problem := map[string]any{"code": code, "message": err.Error()}
	encoded, _ := json.Marshal(problem)
	return &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"problem": problem}, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
}

// runDevelopmentMCPServer serves the development MCP adapter over stdio
// newline-delimited JSON-RPC until the client disconnects.
func runDevelopmentMCPServer(ctx context.Context, in io.Reader, out io.Writer, appRoot string) error {
	root, err := findContractRoot(appRoot)
	if err != nil {
		return err
	}
	return newDevelopmentMCPServer(root).Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
