package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerabi"
)

const durableRunKind = "scenery.durable.run.result"

type durableRunOptions struct {
	root, target, input string
	wait, json          bool
	timeout             time.Duration
}

type durableRunError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	ReportToken string `json:"report_token,omitempty"`
}

type durableRunResult struct {
	cliPayloadIdentity
	callTelemetry
	OK         bool             `json:"ok"`
	JobID      string           `json:"job_id"`
	Target     string           `json:"target"`
	Module     string           `json:"module"`
	Kind       string           `json:"execution_kind"`
	State      string           `json:"state"`
	Attempts   int              `json:"attempts"`
	DurationMS int64            `json:"duration_ms"`
	Input      json.RawMessage  `json:"input"`
	Output     json.RawMessage  `json:"output,omitempty"`
	TraceID    string           `json:"trace_id"`
	TimedOut   bool             `json:"timed_out"`
	Error      *durableRunError `json:"error,omitempty"`
	Report     *failureReport   `json:"report,omitempty"`
}

func parseDurableRunArgs(args []string) (durableRunOptions, error) {
	var opts durableRunOptions
	flags := newCLIFlagSet("durable run")
	flags.StringVar(&opts.root, "app-root", "", "")
	flags.StringVar(&opts.input, "input", "", "")
	flags.BoolVar(&opts.wait, "wait", true, "")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "")
	registerJSONOutput(flags, &opts.json)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, err
	}
	if len(positionals) != 1 || strings.TrimSpace(positionals[0]) == "" {
		return opts, usageErrorf("durable run requires one target")
	}
	opts.target = positionals[0]
	if cliFlagSet(flags, "input") && (!json.Valid([]byte(opts.input)) || len(opts.input) > workerabi.MaxMetadataBytes-1024) {
		return opts, usageErrorf("--input must contain one JSON value within the worker input limit")
	}
	if opts.timeout <= 0 || opts.timeout > time.Hour {
		return opts, usageErrorf("--timeout must be positive and at most 1h")
	}
	return opts, nil
}

func runDurableCommand(ctx context.Context, out io.Writer, args []string) error {
	opts, err := parseDurableRunArgs(args)
	if err != nil {
		return err
	}
	root, err := findContractRoot(opts.root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	endpoint, found, err := readWorkerDevEndpoint(root)
	if err != nil {
		return err
	}
	if !found {
		return preconditionErrorf("durable run requires a running worker development Host for %s", root)
	}
	client := workerControlClient(endpoint.Control)
	client.CheckRedirect = refuseCallRedirect
	defer client.CloseIdleConnections()
	connection := agentCallConnection{client: client, worker: endpoint, controlToken: endpoint.Token}
	result, err := invokeDevDurable(ctx, root, connection, opts)
	if err != nil {
		return err
	}
	if opts.json {
		var diagnostics []graph.Diagnostic
		if result.Error != nil {
			diagnostics = append(diagnostics, graph.Diagnostic{Code: result.Error.Code, Severity: "error", Message: result.Error.Message, ReportToken: result.Error.ReportToken})
		}
		if err := json.NewEncoder(out).Encode(newCLIEnvelope(result.OK, result, diagnostics)); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(out, "%s %s: %s (%d attempts, %dms)\n", result.Target, result.JobID, result.State, result.Attempts, result.DurationMS)
		if result.Output != nil {
			_, _ = fmt.Fprintln(out, string(result.Output))
		}
		if result.Error != nil {
			_, _ = fmt.Fprintln(out, result.Error.Message, result.Error.ReportToken)
		}
	}
	if !result.OK {
		if result.Error == nil {
			return &silentCLIError{err: fmt.Errorf("durable execution failed"), code: 3}
		}
		return &silentCLIError{err: &cliDiagnosticError{code: 3, diagnostic: graph.Diagnostic{
			Code: result.Error.Code, Severity: "error", Message: result.Error.Message, ReportToken: result.Error.ReportToken,
		}}, code: 3}
	}
	return nil
}

func invokeDevDurable(ctx context.Context, root string, connection agentCallConnection, opts durableRunOptions) (durableRunResult, error) {
	started := time.Now()
	result := durableRunResult{cliPayloadIdentity: newCLIPayloadIdentity(durableRunKind), callTelemetry: emptyCallTelemetry()}
	payload, err := json.Marshal(struct {
		Target    string          `json:"target"`
		Input     json.RawMessage `json:"input,omitempty"`
		Wait      bool            `json:"wait"`
		TimeoutNS time.Duration   `json:"timeout_ns"`
	}{opts.target, json.RawMessage(opts.input), opts.wait, opts.timeout})
	if err != nil {
		return result, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, opts.timeout+2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "http://app/__scenery/process/v1/dev-durable", bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.controlToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := connection.client.Do(req)
	if err != nil {
		return result, preconditionErrorf("worker development Host did not respond: %s", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return result, usageErrorf("%s", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return result, preconditionErrorf("durable dispatch rejected by development Host (HTTP %d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&result); err != nil {
		return result, err
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.OK = result.Error == nil
	if opts.wait && !result.TimedOut {
		reportToken := ""
		if result.Error != nil {
			reportToken = result.Error.ReportToken
		}
		spanType := "DURABLE"
		if result.Kind == "schedule" && strings.HasPrefix(result.JobID, "run_") {
			spanType = "SCHEDULE"
		}
		trace := agentCallResult{TraceID: result.TraceID, callTelemetry: result.callTelemetry}
		connection.collect(ctx, root, &trace, reportToken, started, spanType)
		result.callTelemetry, result.Report = trace.callTelemetry, trace.FailureReport
	}
	return result, nil
}
