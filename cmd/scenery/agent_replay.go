package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devreport"
	"scenery.sh/internal/graph"
)

const replayResultKind = "scenery.replay.result"

type agentReplayOptions struct {
	root       string
	outputPath string
	emitTest   bool
	wait       bool
	jsonOutput bool
}

type agentReplayResult struct {
	cliPayloadIdentity
	OK              bool                        `json:"ok"`
	Mode            string                      `json:"mode"`
	ReportToken     string                      `json:"report_token"`
	Status          string                      `json:"status"`
	OriginalCause   string                      `json:"original_cause"`
	Invocation      *devreport.ReplayInvocation `json:"invocation"`
	HTTPStatus      int                         `json:"http_status,omitempty"`
	Body            json.RawMessage             `json:"body,omitempty"`
	BodyEncoding    string                      `json:"body_encoding,omitempty"`
	Outcome         json.RawMessage             `json:"outcome,omitempty"`
	DurationMS      int64                       `json:"duration_ms"`
	Generation      uint64                      `json:"generation,omitempty"`
	TraceID         string                      `json:"trace_id,omitempty"`
	Logs            []devreport.LogEvent        `json:"logs,omitempty"`
	Spans           []devdash.TraceSpanDetail   `json:"spans,omitempty"`
	FailureReport   *failureReport              `json:"failure_report,omitempty"`
	SourceWait      *runtimeSourceWait          `json:"source_wait,omitempty"`
	EmittedTestPath string                      `json:"emitted_test_path,omitempty"`
	NextCommand     string                      `json:"next_command,omitempty"`
}

func runAgentReplay(ctx context.Context, out io.Writer, args []string) error {
	flags := newCLIFlagSet("replay")
	var opts agentReplayOptions
	flags.StringVar(&opts.root, "app-root", "", "")
	flags.BoolVar(&opts.emitTest, "emit-test", false, "")
	flags.StringVar(&opts.outputPath, "output", "", "")
	flags.BoolVar(&opts.wait, "wait", false, "")
	registerJSONOutput(flags, &opts.jsonOutput)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if len(positionals) != 1 {
		return usageErrorf("usage: scenery replay <report-token> [--emit-test] [--output <path>] [--wait] [--app-root <path>] [-o human|json]")
	}
	if opts.outputPath != "" && !opts.emitTest {
		return usageErrorf("--output requires --emit-test")
	}

	token := strings.TrimSpace(positionals[0])
	report, err := readFailureReport(token)
	if err != nil {
		return err
	}
	if report.Origin != "runtime" || report.Invocation == nil {
		return preconditionErrorf("report %s does not contain a replayable runtime invocation", token)
	}
	if report.Invocation.InputTruncated {
		return preconditionErrorf("report %s omitted its input because it exceeded the 256 KiB replay bound", token)
	}

	startRoot := opts.root
	if startRoot == "" && report.WorkingDirectory != "" {
		startRoot = report.WorkingDirectory
	}
	root, err := findContractRoot(startRoot)
	if err != nil {
		return err
	}
	if canonical, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = canonical
	}

	if opts.emitTest {
		return emitReplayRegressionTest(ctx, out, root, report, opts)
	}
	return executeLiveReplay(ctx, out, root, report, opts)
}

func executeLiveReplay(ctx context.Context, out io.Writer, root string, report failureReport, opts agentReplayOptions) error {
	inv := report.Invocation
	res := agentReplayResult{
		cliPayloadIdentity: newCLIPayloadIdentity(replayResultKind),
		Mode:               "live",
		ReportToken:        report.ReportToken,
		OriginalCause:      report.Cause,
		Invocation:         inv,
	}

	connection, err := resolveAgentCallConnection(ctx, root)
	if err != nil {
		return err
	}
	defer connection.client.CloseIdleConnections()

	state, err := connection.waitForSource(ctx, root, opts.wait)
	if err != nil {
		return err
	}
	if !state.OK {
		res.Status = "changed_error"
		res.SourceWait = &state
		return printAgentReplay(out, opts.jsonOutput, res)
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var bearer string
	if !strings.HasPrefix(inv.Principal, "workload:") {
		bearer, err = connection.mint(ctx, agentCallOptions{
			principal: inv.Principal,
			tenant:    inv.TenantID,
		})
		if err != nil {
			return err
		}
	}

	started := time.Now()
	if inv.HTTPMethod == "" && inv.Binding == "" {
		return executeDurableReplay(ctx, out, root, connection, bearer, report, res, started, opts.jsonOutput)
	}

	var (
		response             *http.Response
		isHTTP               bool
		body                 []byte
		isGenerationConflict bool
	)
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			state, err = connection.waitForSource(ctx, root, true)
			if err != nil {
				return err
			}
			if !state.OK {
				res.Status = "changed_error"
				res.SourceWait = &state
				return printAgentReplay(out, opts.jsonOutput, res)
			}
		}
		started = time.Now()
		response, isHTTP, err = invokeReplayTarget(ctx, connection, inv, bearer, state.Serving.Generation)
		if err != nil {
			return err
		}
		body, err = io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		_ = response.Body.Close()
		if err != nil {
			return err
		}
		if len(body) > 8<<20 {
			return fmt.Errorf("failed_precondition: replay response exceeds 8 MiB")
		}
		isGenerationConflict = response.StatusCode == http.StatusConflict &&
			len(body) == 0 &&
			uint64Header(response.Header, "X-App-Generation") == 0
		if !isGenerationConflict {
			break
		}
	}

	res.DurationMS = time.Since(started).Milliseconds()
	res.HTTPStatus = response.StatusCode
	res.TraceID = response.Header.Get("X-Trace-Id")
	res.Generation = uint64Header(response.Header, "X-App-Generation")

	if len(body) > 0 {
		switch {
		case json.Valid(body) && !isHTTP && response.StatusCode >= 200 && response.StatusCode < 300:
			res.Outcome = body
		case json.Valid(body):
			res.Body = body
		case strings.HasPrefix(response.Header.Get("Content-Type"), "text/"):
			res.Body, _ = json.Marshal(string(body))
		default:
			res.Body, _ = json.Marshal(base64.StdEncoding.EncodeToString(body))
			res.BodyEncoding = "base64"
		}
	}

	var problem struct {
		Code        string `json:"code"`
		ReportToken string `json:"report_token"`
	}
	_ = json.Unmarshal(body, &problem)
	newToken := firstNonEmpty(response.Header.Get("X-Report-Token"), problem.ReportToken)

	var callRes agentCallResult
	callRes.TraceID = res.TraceID
	connection.collect(ctx, root, &callRes, newToken, started, "REQUEST")
	res.Logs = callRes.Logs
	res.Spans = callRes.Spans
	res.FailureReport = callRes.FailureReport

	internalFailed := response.StatusCode >= 500 || newToken != "" || problem.Code == "system.internal"
	routeNotFound := response.StatusCode == http.StatusNotFound &&
		response.Header.Get("X-App-Worker-Pid") == "" &&
		res.TraceID == ""
	transportOrAdmissionRejected := isGenerationConflict ||
		routeNotFound ||
		strings.HasPrefix(problem.Code, "transport.") ||
		strings.HasPrefix(problem.Code, "admission.")
	if !internalFailed && !transportOrAdmissionRejected {
		res.OK = true
		res.Status = "fixed"
	} else if internalFailed && res.FailureReport != nil && normalizeFailureCause(res.FailureReport.Cause) == normalizeFailureCause(report.Cause) {
		res.OK = false
		res.Status = "reproduced"
	} else {
		res.OK = false
		res.Status = "changed_error"
	}
	return printAgentReplay(out, opts.jsonOutput, res)
}

func invokeReplayTarget(ctx context.Context, connection agentCallConnection, inv *devreport.ReplayInvocation, bearer string, generation uint64) (*http.Response, bool, error) {
	values := url.Values{}
	var payload []byte
	isHTTP := inv.HTTPMethod != "" && inv.HTTPPath != ""
	if isHTTP {
		values.Set("method", inv.HTTPMethod)
		values.Set("path", inv.HTTPPath)
		if len(inv.HTTPBody) > 0 {
			payload = inv.HTTPBody
		}
	} else {
		values.Set("binding", inv.Binding)
		values.Set("module", inv.Module)
		values.Set("replay", "1")
		if len(inv.Input) > 0 {
			payload = inv.Input
		} else {
			payload = []byte("{}")
		}
	}
	if generation != 0 {
		values.Set("generation", strconv.FormatUint(generation, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://app/__scenery/process/v1/dev-call?"+values.Encode(), bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	for name, vals := range inv.Headers {
		lower := strings.ToLower(name)
		if lower == "accept-encoding" || lower == "content-encoding" || lower == "content-length" || lower == "transfer-encoding" || lower == "connection" || strings.HasPrefix(lower, "x-forwarded-") {
			continue
		}
		for _, v := range vals {
			req.Header.Add(name, v)
		}
	}
	if len(payload) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+connection.controlToken)
	if bearer != "" {
		req.Header.Set("X-App-Invocation-Authorization", bearer)
	}
	resp, err := connection.client.Do(req)
	return resp, isHTTP, err
}

func resolveReplayRefAddress(owner graph.Resource, raw any, defaultKind string) string {
	refMap, _ := raw.(map[string]any)
	ref := firstNonEmpty(stringValueFromMap(refMap, "$ref"), fmt.Sprint(raw))
	if raw == nil {
		return ""
	}
	if _, ok := raw.(string); !ok && refMap == nil {
		return ""
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "/") {
		return ref
	}
	if kind, name, hasDot := strings.Cut(ref, "."); hasDot && kind == defaultKind {
		return owner.Module + "/" + kind + "/" + name
	}
	return ref
}

func resolveDurableReplayTarget(ctx context.Context, root string, inv *devreport.ReplayInvocation) string {
	if strings.TrimSpace(inv.Target) != "" {
		return strings.TrimSpace(inv.Target)
	}
	fallback := inv.Module + "." + inv.Operation[strings.LastIndex(inv.Operation, "/")+1:]
	g, err := loadAgentGraph(ctx, root)
	if err != nil || g.Manifest == nil {
		return fallback
	}
	findSchedule := func() string {
		for _, r := range g.Manifest.Resources {
			if r.Kind != "scenery.schedule" {
				continue
			}
			invoke, _ := r.Spec["invoke"].(map[string]any)
			opAddr := firstNonEmpty(
				resolveReplayRefAddress(r, invoke["operation"], "operation"),
				resolveReplayRefAddress(r, r.Spec["operation"], "operation"),
			)
			if opAddr == inv.Operation {
				return r.Address
			}
		}
		return ""
	}
	findDurableExecution := func() string {
		for _, r := range g.Manifest.Resources {
			if r.Kind != "scenery.execution" || stringValueFromMap(r.Spec, "mode") != "durable" {
				continue
			}
			if resolveReplayRefAddress(r, r.Spec["operation"], "operation") == inv.Operation {
				return r.Address
			}
		}
		return ""
	}
	if inv.Mode == "durable" {
		if addr := findDurableExecution(); addr != "" {
			return addr
		}
		if addr := findSchedule(); addr != "" {
			return addr
		}
		return fallback
	}
	if addr := findSchedule(); addr != "" {
		return addr
	}
	if addr := findDurableExecution(); addr != "" {
		return addr
	}
	return fallback
}

func stringValueFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func executeDurableReplay(ctx context.Context, out io.Writer, root string, connection agentCallConnection, bearer string, report failureReport, res agentReplayResult, started time.Time, jsonOutput bool) error {
	inv := report.Invocation
	reqBody, err := json.Marshal(map[string]any{
		"target":     resolveDurableReplayTarget(ctx, root, inv),
		"input":      json.RawMessage(firstNonEmpty(string(inv.Input), "{}")),
		"wait":       true,
		"replay":     true,
		"timeout_ns": int64((30 * time.Second).Nanoseconds()),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://app/__scenery/process/v1/dev-durable", bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+connection.controlToken)
	if bearer != "" {
		req.Header.Set("X-App-Invocation-Authorization", bearer)
	}
	resp, err := connection.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	res.DurationMS = time.Since(started).Milliseconds()
	res.HTTPStatus = resp.StatusCode
	res.Body = raw

	var durableReply struct {
		State   string `json:"state"`
		TraceID string `json:"trace_id"`
		Error   *struct {
			Code        string `json:"code"`
			ReportToken string `json:"report_token"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &durableReply)
	res.TraceID = durableReply.TraceID
	var newToken string
	if durableReply.Error != nil {
		newToken = durableReply.Error.ReportToken
	}
	var callRes agentCallResult
	callRes.TraceID = res.TraceID
	connection.collect(ctx, root, &callRes, newToken, started, "DURABLE")
	res.Logs = callRes.Logs
	res.Spans = callRes.Spans
	res.FailureReport = callRes.FailureReport

	if resp.StatusCode == http.StatusOK && durableReply.State == "succeeded" {
		res.OK = true
		res.Status = "fixed"
	} else if res.FailureReport != nil && normalizeFailureCause(res.FailureReport.Cause) == normalizeFailureCause(report.Cause) {
		res.OK = false
		res.Status = "reproduced"
	} else {
		res.OK = false
		res.Status = "changed_error"
	}
	return printAgentReplay(out, jsonOutput, res)
}

func normalizeFailureCause(cause string) string {
	return strings.TrimSpace(cause)
}

func printAgentReplay(out io.Writer, jsonOutput bool, result agentReplayResult) error {
	if result.Logs == nil {
		result.Logs = []devreport.LogEvent{}
	}
	if result.Spans == nil {
		result.Spans = []devdash.TraceSpanDetail{}
	}
	if jsonOutput {
		if err := writeCLIJSON(out, result); err != nil {
			return err
		}
	} else if result.Mode == "emit_test" {
		_, _ = fmt.Fprintf(out, "emitted regression test %s for %s (%s)\nnext: %s\n",
			result.EmittedTestPath, result.ReportToken, result.Invocation.Module, result.NextCommand)
	} else if result.SourceWait != nil {
		_, _ = fmt.Fprintf(out, "replay %s: source wait %s\n%s\n", result.ReportToken, result.SourceWait.Outcome, result.SourceWait.CompileError)
	} else {
		_, _ = fmt.Fprintf(out, "replay %s (%s): %s in %dms (HTTP %d, trace %s)\n",
			result.ReportToken, result.Invocation.Operation, result.Status, result.DurationMS, result.HTTPStatus, result.TraceID)
		if result.FailureReport != nil {
			_, _ = fmt.Fprintf(out, "cause (%s): %s\n", result.FailureReport.ReportToken, result.FailureReport.Cause)
		}
	}
	if !result.OK {
		err := &silentCLIError{err: fmt.Errorf("replay %s: %s", result.ReportToken, result.Status), code: 3}
		if result.FailureReport != nil {
			err.err = &cliDiagnosticError{err: err.err, code: 3, diagnostic: graph.Diagnostic{
				Code: result.FailureReport.Code, Severity: "error", Message: "replay reproduced internal failure", ReportToken: result.FailureReport.ReportToken,
			}}
		}
		return err
	}
	return nil
}
