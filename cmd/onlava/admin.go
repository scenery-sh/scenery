package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
)

type adminOptions struct {
	Domain  string
	Action  string
	AppRoot string
	JSON    bool
}

type adminResponse struct {
	SchemaVersion string         `json:"schema_version"`
	OK            bool           `json:"ok"`
	Command       string         `json:"command"`
	App           adminAppRef    `json:"app"`
	Warnings      []string       `json:"warnings,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
}

type adminAppRef struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

var dashboardWSDialer = websocket.DefaultDialer

func adminCommand(args []string) error {
	return runOnlavaAdmin(context.Background(), args, os.Stdout)
}

func runOnlavaAdmin(ctx context.Context, args []string, stdout io.Writer) error {
	opts, err := parseAdminArgs(args)
	if err != nil {
		return err
	}
	if !opts.JSON {
		return fmt.Errorf("onlava admin currently requires --json")
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	appID := cfg.AppID()

	resp := adminResponse{
		SchemaVersion: "onlava.admin.result.v1",
		OK:            true,
		Command:       "onlava admin " + opts.Domain + " " + opts.Action,
		App: adminAppRef{
			Name: cfg.Name,
			Root: appRoot,
		},
	}

	switch opts.Domain + "/" + opts.Action {
	case "traces/clear":
		store, err := devdash.OpenStore(os.Getenv("ONLAVA_DEV_CACHE_DIR"))
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.ClearTraces(ctx, appID); err != nil {
			return err
		}
		resp.Data = map[string]any{
			"app_id":  appID,
			"cleared": "traces",
		}
	case "pubsub/clear":
		result, err := callDashboardRPC(ctx, "pubsub/clear", map[string]any{"app_id": appID})
		if err != nil {
			return err
		}
		data, err := rpcResultMap(result)
		if err != nil {
			return err
		}
		resp.Data = data
	default:
		return fmt.Errorf("unsupported admin command %q", opts.Domain+" "+opts.Action)
	}

	return writeAdminJSON(stdout, resp)
}

func parseAdminArgs(args []string) (adminOptions, error) {
	if len(args) < 2 {
		return adminOptions{}, fmt.Errorf("usage: onlava admin traces clear --json [--app-root <path>] | onlava admin pubsub clear --json [--app-root <path>]")
	}
	opts := adminOptions{
		Domain: args[0],
		Action: args[1],
	}
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--app-root":
			i++
			if i >= len(args) {
				return adminOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			return adminOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func writeAdminJSON(w io.Writer, payload adminResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func callDashboardRPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	wsURL := url.URL{Scheme: "ws", Host: devdash.ListenAddr(), Path: devdash.WebSocketPath}
	conn, resp, err := dashboardWSDialer.DialContext(ctx, wsURL.String(), http.Header{})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("onlava dashboard RPC unavailable: %s", resp.Status)
		}
		return nil, fmt.Errorf("onlava dashboard RPC unavailable: %w", err)
	}
	defer conn.Close()

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      "onlava-admin",
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		req.Params = raw
	}
	if err := conn.WriteJSON(req); err != nil {
		return nil, err
	}
	var respMsg rpcResponse
	if err := conn.ReadJSON(&respMsg); err != nil {
		return nil, err
	}
	if respMsg.Error != nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(respMsg.Error.Message))
	}
	data, err := json.Marshal(respMsg.Result)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func rpcResultMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return data, nil
}
