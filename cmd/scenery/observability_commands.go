package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

func tracesCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery traces list [-o json] [--app-root <path>]")
	}
	if err := flagBeforeWord(args, "the traces subcommand"); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return runObservabilityList(context.Background(), os.Stdout, "traces", args[1:])
	default:
		return usageErrorf("unknown traces command %q", args[0])
	}
}

func metricsCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery metrics list|query [-o json] [--app-root <path>]")
	}
	if err := flagBeforeWord(args, "the metrics subcommand"); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return runObservabilityList(context.Background(), os.Stdout, "metrics", args[1:])
	case "query":
		return runMetricsQueryCommand(context.Background(), os.Stdout, args[1:])
	default:
		return usageErrorf("unknown metrics command %q", args[0])
	}
}

func runObservabilityList(ctx context.Context, stdout io.Writer, subject string, args []string) error {
	opts, err := parseInspectArgsInternal(append([]string{subject}, args...), true)
	if err != nil {
		return inspectInvalidRequest(err)
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
		return usageErrorf("unknown observability subject %q", subject)
	}
}
