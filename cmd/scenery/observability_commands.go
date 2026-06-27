package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

type adminOptions struct {
	AppRoot string
	JSON    bool
}

func tracesCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery traces list|clear [--json] [--app-root <path>]")
	}
	switch args[0] {
	case "list":
		return runObservabilityList(context.Background(), os.Stdout, "traces", args[1:])
	case "clear":
		return runTracesClear(context.Background(), os.Stdout, args[1:])
	default:
		return fmt.Errorf("unknown traces command %q", args[0])
	}
}

func metricsCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery metrics list|query|labels|series [--json] [--app-root <path>]")
	}
	switch args[0] {
	case "list":
		return runObservabilityList(context.Background(), os.Stdout, "metrics", args[1:])
	case "query":
		return runMetricsQueryCommand(context.Background(), os.Stdout, args[1:])
	case "labels":
		return runMetricsLabelsCommand(context.Background(), os.Stdout, args[1:])
	case "series":
		return runMetricsSeriesCommand(context.Background(), os.Stdout, args[1:])
	default:
		return fmt.Errorf("unknown metrics command %q", args[0])
	}
}

func runObservabilityList(ctx context.Context, stdout io.Writer, subject string, args []string) error {
	opts, err := parseInspectArgsInternal(append([]string{subject}, args...), true)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(start)
	if err != nil {
		return err
	}
	switch subject {
	case "traces":
		resp, err := buildInspectTracesResponse(ctx, appRoot, cfg, opts.Trace)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		for _, trace := range resp.Traces {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%.3fms\n", trace.TraceID, trace.Status, trace.Service, trace.Endpoint, trace.DurationMS); err != nil {
				return err
			}
		}
		return nil
	case "metrics":
		resp, err := buildInspectMetricsResponse(ctx, appRoot, cfg, opts.Trace)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, resp)
		}
		_, err = fmt.Fprintf(stdout, "traces=%d errors=%d error_rate=%.4f logs=%d avg=%.3fms p95=%.3fms\n", resp.Summary.TraceCount, resp.Summary.ErrorCount, resp.Summary.ErrorRate, resp.Summary.LogCount, resp.Summary.AvgDurationMS, resp.Summary.P95DurationMS)
		return err
	default:
		return fmt.Errorf("unknown observability subject %q", subject)
	}
}

func runTracesClear(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseTracesClearArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(start)
	if err != nil {
		return err
	}
	appID := cfg.AppID()
	if stack := defaultVictoriaQueryStack(); stack != nil {
		stack.MarkCleared(appID, time.Now().UTC())
	}
	resp := struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Command       string `json:"command"`
		App           struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"app"`
		Data map[string]any `json:"data,omitempty"`
	}{
		SchemaVersion: "scenery.traces.clear.v1",
		OK:            true,
		Command:       "scenery traces clear",
		Data: map[string]any{
			"app_id":  appID,
			"cleared": "traces",
		},
	}
	resp.App.Name = cfg.Name
	resp.App.Root = appRoot
	if opts.JSON {
		return writeInspectJSON(stdout, resp)
	}
	_, err = fmt.Fprintf(stdout, "cleared traces for %s\n", appID)
	return err
}

func parseTracesClearArgs(args []string) (adminOptions, error) {
	opts := adminOptions{}
	for i := 0; i < len(args); i++ {
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
