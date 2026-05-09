package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
)

type logsOptions struct {
	AppRoot string
	Limit   int
	Follow  bool
	Stream  string
	JSONL   bool
}

type logsEvent struct {
	SchemaVersion string `json:"schema_version"`
	App           struct {
		Name string `json:"name"`
		Root string `json:"root"`
	} `json:"app"`
	ID        int64  `json:"id"`
	PID       string `json:"pid"`
	Stream    string `json:"stream"`
	Output    string `json:"output"`
	CreatedAt string `json:"created_at"`
}

func logsCommand(args []string) error {
	return runOnlavaLogs(context.Background(), os.Stdout, args)
}

func runOnlavaLogs(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
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

	store, err := devdash.OpenStore(os.Getenv("ONLAVA_DEV_CACHE_DIR"))
	if err != nil {
		return err
	}
	defer store.Close()

	record, err := store.GetApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("no local logs found for %q; run `onlava run` first", appID)
	}
	if record.Root != "" && record.Root != appRoot {
		return fmt.Errorf("local logs for %q belong to %s, not %s", appID, record.Root, appRoot)
	}

	items, err := store.ListProcessOutput(ctx, appID, opts.Limit)
	if err != nil {
		return err
	}
	lastID := int64(0)
	for _, item := range items {
		if item.ID > lastID {
			lastID = item.ID
		}
		if streamAllowed(opts.Stream, item.Stream) {
			if err := writeProcessOutput(stdout, appID, appRoot, item, opts.JSONL); err != nil {
				return err
			}
		}
	}

	if !opts.Follow {
		return nil
	}

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			items, err := store.ListProcessOutputSince(ctx, appID, lastID, 200)
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ID > lastID {
					lastID = item.ID
				}
				if streamAllowed(opts.Stream, item.Stream) {
					if err := writeProcessOutput(stdout, appID, appRoot, item, opts.JSONL); err != nil {
						return err
					}
				}
			}
		}
	}
}

func parseLogsArgs(args []string) (logsOptions, error) {
	opts := logsOptions{
		Limit:  200,
		Stream: "all",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--limit", "-n":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for %s", args[i-1])
			}
			value, err := strconv.Atoi(args[i])
			if err != nil || value <= 0 {
				return logsOptions{}, fmt.Errorf("invalid limit %q", args[i])
			}
			opts.Limit = value
		case "--follow", "-f":
			opts.Follow = true
		case "--jsonl", "--json":
			opts.JSONL = true
		case "--stream":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --stream")
			}
			opts.Stream = normalizeLogStream(args[i])
			if opts.Stream == "" {
				return logsOptions{}, fmt.Errorf("invalid stream %q", args[i])
			}
		default:
			return logsOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func normalizeLogStream(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all", "":
		return "all"
	case "stdout":
		return "stdout"
	case "stderr":
		return "stderr"
	default:
		return ""
	}
}

func streamAllowed(filter, stream string) bool {
	return filter == "all" || filter == stream
}

func writeProcessOutput(w io.Writer, appName, appRoot string, item devdash.ProcessOutput, jsonl bool) error {
	if jsonl {
		return writeLogsJSONL(w, appName, appRoot, item)
	}
	_, err := w.Write(item.Output)
	return err
}

func writeLogsJSONL(w io.Writer, appName, appRoot string, item devdash.ProcessOutput) error {
	event := logsEvent{
		SchemaVersion: "onlava.logs.event.v1",
		ID:            item.ID,
		PID:           item.PID,
		Stream:        item.Stream,
		Output:        string(item.Output),
		CreatedAt:     item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	event.App.Name = appName
	event.App.Root = appRoot
	enc := json.NewEncoder(w)
	return enc.Encode(event)
}
