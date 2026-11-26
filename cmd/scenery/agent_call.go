package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/victoria"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devreport"
	"scenery.sh/internal/graph"
	obs "scenery.sh/internal/observability"
)

type agentCallOptions struct {
	root, input, inputFile, principal, tenant string
	wait, jsonOutput                          bool
}

type agentCallResult struct {
	cliPayloadIdentity
	Target       string          `json:"target"`
	Binding      string          `json:"binding,omitempty"`
	HTTPStatus   int             `json:"http_status"`
	OK           bool            `json:"ok"`
	Body         json.RawMessage `json:"body,omitempty"`
	BodyEncoding string          `json:"body_encoding,omitempty"`
	Outcome      json.RawMessage `json:"outcome,omitempty"`
	Headers      http.Header     `json:"headers"`
	DurationMS   int64           `json:"duration_ms"`
	Generation   uint64          `json:"generation"`
	WorkerPID    int             `json:"worker_pid"`
	HostPID      int             `json:"host_pid"`
	TraceID      string          `json:"trace_id"`
	callTelemetry
	FailureReport *failureReport     `json:"failure_report,omitempty"`
	SourceWait    *runtimeSourceWait `json:"source_wait,omitempty"`
}

func runAgentCall(ctx context.Context, out io.Writer, args []string) error {
	flags := newCLIFlagSet("call")
	var opts agentCallOptions
	flags.StringVar(&opts.root, "app-root", "", "")
	flags.StringVar(&opts.input, "input", "", "")
	flags.StringVar(&opts.inputFile, "input-file", "", "")
	flags.StringVar(&opts.principal, "as", "", "")
	flags.StringVar(&opts.tenant, "tenant", "", "")
	flags.BoolVar(&opts.wait, "wait", false, "")
	registerJSONOutput(flags, &opts.jsonOutput)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return usageErrorf("%v", err)
	}
	used := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) { used[flag.Name] = true })
	if len(positionals) != 1 || used["input"] && used["input-file"] {
		return usageErrorf("usage: scenery call <target> [--input <json>|--input-file <path>]")
	}
	if used["as"] && strings.TrimSpace(opts.principal) == "" || used["tenant"] && strings.TrimSpace(opts.tenant) == "" {
		return usageErrorf("--as and --tenant require nonempty values")
	}
	input, err := readAgentCallInput(opts, used["input"], used["input-file"])
	if err != nil {
		return err
	}
	root, err := findContractRoot(opts.root)
	if err != nil {
		return err
	}
	res := agentCallResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.call.result"), Target: positionals[0],
		Headers: make(http.Header), callTelemetry: emptyCallTelemetry(),
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
		res.SourceWait = &state
		return printAgentCall(out, opts.jsonOutput, res)
	}
	g, err := loadAgentGraph(ctx, root)
	if err != nil {
		return err
	}
	if state.Serving.ContractRevision != g.Manifest.ContractRevision {
		return fmt.Errorf("revision_conflict: the application contract changed after the source wait")
	}
	binding, route, err := g.callTarget(res.Target, opts.principal != "" || opts.tenant != "")
	if err != nil {
		return err
	}
	res.Binding = binding.Address
	if route.Method == "" && connection.controlToken == "" {
		linked, err := connection.internal(ctx, root)
		if err != nil {
			return err
		}
		connection = linked
		defer connection.client.CloseIdleConnections()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	bearer, err := connection.mint(ctx, opts)
	if err != nil {
		return err
	}
	started := time.Now()
	response, err := connection.invoke(ctx, route.Method, route.Path, binding, bearer, input, state.Serving.Generation)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(body) > 8<<20 {
		return fmt.Errorf("failed_precondition: call response exceeds 8 MiB")
	}
	res.DurationMS = time.Since(started).Milliseconds()
	res.HTTPStatus, res.OK, res.Headers = response.StatusCode, response.StatusCode >= 200 && response.StatusCode < 300, response.Header
	res.TraceID = response.Header.Get("X-Trace-Id")
	res.Generation = uint64Header(response.Header, "X-App-Generation", "X-Scenery-Process-Generation")
	res.HostPID = int(uint64Header(response.Header, "X-App-Host-Pid", "X-Scenery-Process-Id"))
	res.WorkerPID = int(uint64Header(response.Header, "X-App-Worker-Pid", "X-Scenery-Service-Process-Id"))
	if len(body) > 0 {
		switch {
		case json.Valid(body) && route.Method == "" && res.OK:
			res.Outcome = body
			var outcome struct{ Kind string }
			if json.Unmarshal(body, &outcome) == nil && outcome.Kind == "error" {
				res.OK = false
			}
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
		ReportToken string `json:"report_token"`
	}
	_ = json.Unmarshal(body, &problem)
	connection.collect(ctx, root, &res, firstNonEmpty(response.Header.Get("X-Report-Token"), problem.ReportToken), started, "REQUEST")
	return printAgentCall(out, opts.jsonOutput, res)
}

func readAgentCallInput(opts agentCallOptions, inline, file bool) ([]byte, error) {
	var data []byte
	switch {
	case file:
		f, err := os.Open(opts.inputFile)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		data, err = io.ReadAll(io.LimitReader(f, (8<<20)+1))
		if err != nil {
			return nil, err
		}
	case inline:
		data = []byte(opts.input)
	default:
		return nil, nil
	}
	if len(data) > 8<<20 || !json.Valid(data) {
		return nil, usageErrorf("input must be one JSON value of at most 8 MiB")
	}
	return data, nil
}

func printAgentCall(out io.Writer, jsonOutput bool, result agentCallResult) error {
	if jsonOutput {
		if err := writeCLIJSON(out, result); err != nil {
			return err
		}
	} else if result.SourceWait != nil {
		_, _ = fmt.Fprintf(out, "%s: source wait %s\n%s\n", result.Target, result.SourceWait.Outcome, result.SourceWait.CompileError)
	} else {
		_, _ = fmt.Fprintf(out, "%s: HTTP %d in %dms (generation %d, trace %s)\n", result.Target, result.HTTPStatus, result.DurationMS, result.Generation, result.TraceID)
		if body := firstNonEmpty(string(result.Body), string(result.Outcome)); body != "" {
			_, _ = fmt.Fprintln(out, body)
		}
		if result.FailureReport != nil {
			_, _ = fmt.Fprintln(out, result.FailureReport.Cause)
		}
	}
	if !result.OK {
		err := &silentCLIError{err: fmt.Errorf("call failed"), code: 3}
		if result.FailureReport != nil {
			err.err = &cliDiagnosticError{err: err.err, code: 3, diagnostic: graph.Diagnostic{
				Code: result.FailureReport.Code, Severity: "error", Message: "call failed", ReportToken: result.FailureReport.ReportToken,
			}}
		}
		return err
	}
	return nil
}

func (g agentGraph) callTarget(target string, asPrincipal bool) (graph.Resource, agentRoute, error) {
	if binding := g.resources[target]; binding.Kind == "scenery.binding" {
		switch binding.Spec["protocol"] {
		case "http":
			return binding, g.route(binding), nil
		case "internal":
			return binding, agentRoute{}, nil
		default:
			return binding, agentRoute{}, usageErrorf("unsupported binding protocol for %q", target)
		}
	}
	method, path, routeTarget := strings.Cut(target, " ")
	if routeTarget {
		routeURL, err := url.ParseRequestURI(path)
		if err != nil || routeURL.IsAbs() || routeURL.Host != "" {
			return graph.Resource{}, agentRoute{}, usageErrorf("invalid application route %q", path)
		}
		method = strings.ToUpper(method)
		type routeCandidate struct {
			resource graph.Resource
			route    agentRoute
			pattern  string
		}
		var matches []routeCandidate
		for _, resource := range g.Manifest.Resources {
			if resource.Kind != "scenery.binding" || resource.Spec["protocol"] != "http" {
				continue
			}
			route := g.route(resource)
			if route.Method == method && agentRouteMatches(route.Path, routeURL.EscapedPath()) {
				pattern := route.Path
				route.Path = path
				matches = append(matches, routeCandidate{resource: resource, route: route, pattern: pattern})
			}
		}
		if len(matches) == 0 {
			return graph.Resource{}, agentRoute{}, usageErrorf("HTTP route %q is not declared", target)
		}
		slices.SortStableFunc(matches, func(a, b routeCandidate) int {
			return compareAgentRoutePatterns(a.pattern, b.pattern)
		})
		return matches[0].resource, matches[0].route, nil
	}
	var matches []graph.Resource
	for _, op := range g.Manifest.Resources {
		if op.Kind != "scenery.operation" {
			continue
		}
		service := g.ref(op, "service", "service")
		handler, _ := op.Spec["handler"].(map[string]any)
		if target == op.Address || target == op.Module+"."+op.Name || target == service.Name+"."+inspectString(handler["method"]) {
			matches = append(matches, op)
		}
	}
	if len(matches) != 1 {
		return graph.Resource{}, agentRoute{}, usageErrorf("target %q must identify one operation or binding", target)
	}
	type candidate struct {
		resource graph.Resource
		route    agentRoute
		rank     int
	}
	var bindings []candidate
	for _, resource := range g.Manifest.Resources {
		if resource.Kind != "scenery.binding" || g.ref(resource, "operation", "operation").Address != matches[0].Address {
			continue
		}
		switch resource.Spec["protocol"] {
		case "http":
			route := g.route(resource)
			rank := 0
			if (inspectBindingAccess(resource) == "auth") != asPrincipal {
				rank++
			}
			bindings = append(bindings, candidate{resource, route, rank})
		case "internal":
			bindings = append(bindings, candidate{resource: resource, rank: 2})
		}
	}
	if len(bindings) == 0 {
		return graph.Resource{}, agentRoute{}, usageErrorf("operation %q has no HTTP or internal binding", target)
	}
	slices.SortStableFunc(bindings, func(a, b candidate) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return len(a.route.Path) - len(b.route.Path)
	})
	return bindings[0].resource, bindings[0].route, nil
}

func agentRouteMatches(pattern, path string) bool {
	want, actual := strings.Split(pattern, "/"), strings.Split(path, "/")
	for index, part := range want {
		if index >= len(actual) {
			return false
		}
		if strings.HasPrefix(part, "*") || strings.HasSuffix(part, "...}") {
			return true
		}
		isParameter := strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")
		if part != actual[index] && (!isParameter || actual[index] == "") {
			return false
		}
	}
	return len(want) == len(actual)
}

func compareAgentRoutePatterns(a, b string) int {
	left, leftLiterals, leftWildcard := parseAgentRoutePattern(a)
	right, rightLiterals, rightWildcard := parseAgentRoutePattern(b)
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] != right[index] {
			return left[index] - right[index]
		}
	}
	if leftLiterals != rightLiterals {
		return rightLiterals - leftLiterals
	}
	if leftWildcard != rightWildcard {
		if leftWildcard {
			return 1
		}
		return -1
	}
	if len(left) != len(right) {
		return len(right) - len(left)
	}
	return 0
}

func parseAgentRoutePattern(path string) (kinds []int, literals int, hasWildcard bool) {
	if path == "" || path == "/" {
		return nil, 0, false
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		switch {
		case strings.HasPrefix(part, "*") || strings.HasSuffix(part, "...}"):
			kinds = append(kinds, 2)
			hasWildcard = true
		case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
			kinds = append(kinds, 1)
		default:
			kinds = append(kinds, 0)
			literals++
		}
	}
	return kinds, literals, hasWildcard
}

type agentCallConnection struct {
	client       *http.Client
	worker       workerDevEndpoint
	session      localagent.Session
	controlToken string
}

func resolveAgentCallConnection(ctx context.Context, root string) (agentCallConnection, error) {
	worker, found, err := readWorkerDevEndpoint(root)
	if err != nil {
		return agentCallConnection{}, err
	}
	if found {
		client := workerControlClient(worker.Control)
		client.CheckRedirect = refuseCallRedirect
		return agentCallConnection{client: client, worker: worker, controlToken: worker.Token}, nil
	}
	control, err := commandWorktreeClient(ctx, root)
	if err != nil {
		return agentCallConnection{}, err
	}
	defer control.CloseIdleConnections()
	sessions, err := control.List(ctx, root)
	if err != nil {
		return agentCallConnection{}, err
	}
	if len(sessions) != 1 || sessions[0].AppRoot != root || !sessionOwnerLive(sessions[0]) {
		return agentCallConnection{}, fmt.Errorf("failed_precondition: scenery call requires a running development session for %s", root)
	}
	session := sessions[0]
	backend := session.Backends[localagent.RouteAPI]
	if (backend.Network != "unix" && backend.Network != "tcp") || backend.Addr == "" {
		return agentCallConnection{}, fmt.Errorf("failed_precondition: local API backend is unavailable")
	}
	return agentCallConnection{client: localCallClient(backend.Network, backend.Addr), session: session}, nil
}

func localCallClient(network, address string) *http.Client {
	return &http.Client{CheckRedirect: refuseCallRedirect, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}}
}

// The default fast path checks the foreground worker's current source
// once. A stale snapshot takes the same bounded path as explicit --wait.
func (c agentCallConnection) waitForSource(ctx context.Context, root string, wait bool) (runtimeSourceWait, error) {
	if c.worker.Socket != "" && !wait {
		var status runtimeSourceWait
		probe, cancel := context.WithTimeout(ctx, time.Second)
		err := workerDevGet(probe, c.worker.Socket, "/status", &status)
		cancel()
		if err == nil && status.cliPayloadIdentity == newCLIPayloadIdentity(devRuntimeWaitKind) && (status.OK || status.Outcome == "failed") {
			return status, nil
		}
	}
	return waitForWorktreeSource(ctx, root, defaultSourceWaitTimeout)
}

func refuseCallRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func (c agentCallConnection) internal(ctx context.Context, root string) (agentCallConnection, error) {
	status, err := readWorktreeRuntimeStatus(ctx, root, c.session)
	if err != nil {
		return c, err
	}
	path := status.ProcessLinkPath
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || filepath.Base(path) != devProcessLinkFile {
		return c, fmt.Errorf("failed_precondition: the development process link is unavailable")
	}
	var link struct {
		Token    string
		Dispatch struct{ Network, Address string }
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &link); err != nil {
		return c, err
	}
	if link.Token == "" || link.Dispatch.Network != "unix" || filepath.Dir(link.Dispatch.Address) != filepath.Dir(path) {
		return c, fmt.Errorf("failed_precondition: invalid local development process link")
	}
	c.client, c.controlToken = localCallClient("unix", link.Dispatch.Address), link.Token
	return c, nil
}

func (c agentCallConnection) mint(ctx context.Context, opts agentCallOptions) (string, error) {
	if opts.principal == "" && opts.tenant == "" {
		return "", nil
	}
	input, err := json.Marshal(map[string]string{"user_id": opts.principal, "tenant_id": opts.tenant})
	if err != nil {
		return "", err
	}
	response, err := c.invoke(ctx, http.MethodPost, "/users/dev-bootstrap", graph.Resource{}, "", input, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	var session struct{ Token string }
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&session) != nil || session.Token == "" {
		return "", fmt.Errorf("failed_precondition: local development session minting failed (HTTP %d)", response.StatusCode)
	}
	return "Bearer " + session.Token, nil
}

func (c agentCallConnection) invoke(ctx context.Context, method, path string, binding graph.Resource, bearer string, input []byte, generation uint64) (*http.Response, error) {
	if strings.ContainsAny(path, "{}") {
		return nil, usageErrorf("supply a concrete HTTP route with path parameters filled in")
	}
	if c.controlToken != "" {
		values := url.Values{}
		if method == "" {
			values.Set("binding", binding.Address)
			values.Set("module", binding.Module)
			if input == nil {
				input = []byte("{}")
			}
		} else {
			values.Set("method", method)
			values.Set("path", path)
		}
		if generation != 0 {
			values.Set("generation", strconv.FormatUint(generation, 10))
		}
		method, path = http.MethodPost, "/__scenery/process/v1/dev-call?"+values.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://app"+path, bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	if len(input) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.controlToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.controlToken)
		// The Host authenticates any invocation header, even an empty one,
		// so a call without --as sends none.
		if bearer != "" {
			req.Header.Set("X-App-Invocation-Authorization", bearer)
		}
	} else if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	return c.client.Do(req)
}

func uint64Header(headers http.Header, names ...string) uint64 {
	for _, name := range names {
		if value, err := strconv.ParseUint(headers.Get(name), 10, 64); err == nil {
			return value
		}
	}
	return 0
}

func (c agentCallConnection) collect(ctx context.Context, root string, result *agentCallResult, reportToken string, started time.Time, spanType string) {
	trace, err := hex.DecodeString(result.TraceID)
	if err != nil || len(trace) != 16 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var scope obs.QueryScope
	var stack *victoria.Stack
	if c.worker.Socket == "" {
		stack = resolveLogsVictoriaStack(ctx, root)
	}
	if stack != nil {
		scope = obs.QueryScope{AppID: c.session.BaseAppID, SessionID: c.session.SessionID, AppRoot: root, Enforced: true}
	}
	for {
		if c.worker.Socket != "" {
			var telemetry callTelemetry
			if workerDevGet(ctx, c.worker.Socket, "/trace?id="+result.TraceID, &telemetry) == nil {
				result.callTelemetry = telemetry
			}
		} else if stack != nil {
			detail, err := stack.GetTraceDetail(ctx, devdash.TraceQuery{AppID: scope.AppID, SessionID: scope.SessionID, TraceID: result.TraceID})
			if err == nil && detail != nil {
				result.Spans = detail.Spans
			}
		}
		if reportToken != "" && result.FailureReport == nil {
			if report, err := readFailureReport(reportToken); err == nil && report.Origin == "runtime" && firstNonEmpty(c.worker.SessionID, c.session.SessionID) == report.SessionID {
				result.FailureReport = &report
			}
		}
		requestComplete := slices.ContainsFunc(result.Spans, func(span devdash.TraceSpanDetail) bool { return span.Type == spanType && span.IsRoot })
		if (requestComplete || (c.worker.Socket == "" && stack == nil)) && (reportToken == "" || result.FailureReport != nil) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	if stack != nil {
		logs, err := obs.QueryLogs(ctx, obs.LogsQuery{
			BaseURL: stack.BaseURL("logs"), Scope: scope, Query: "trace_id:=" + strconv.Quote(result.TraceID),
			Bounds: obs.TimeBounds{Start: started.Add(-time.Second), End: time.Now()}, Limit: 200,
		})
		if err == nil {
			for _, log := range logs.Logs {
				if log.TraceID == result.TraceID {
					at, _ := time.Parse(time.RFC3339Nano, log.Time)
					result.Logs = append(result.Logs, devreport.LogEvent{AppID: scope.AppID, SessionID: scope.SessionID,
						TraceID: log.TraceID, SpanID: log.SpanID, Timestamp: at, Level: log.Level, Message: log.Message, Attrs: log.Fields})
				}
			}
		}
	}
}
