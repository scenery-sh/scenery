package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

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
	default:
		return fmt.Errorf("unsupported admin command %q", opts.Domain+" "+opts.Action)
	}

	return writeAdminJSON(stdout, resp)
}

func parseAdminArgs(args []string) (adminOptions, error) {
	if len(args) < 2 {
		return adminOptions{}, fmt.Errorf("usage: onlava admin traces clear --json [--app-root <path>]")
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
