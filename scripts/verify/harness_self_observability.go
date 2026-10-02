package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	obs "scenery.sh/internal/observability"
	"scenery.sh/internal/victoria"
)

func runHarnessObservabilityProbeStep(ctx context.Context, repo string) harnessStep {
	started := time.Now()
	proof, err := runHarnessObservabilityProbe(ctx, repo)
	step := harnessStep{Name: "observability signal round trip", Summary: proof, OK: err == nil, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

// The probe uses the authored SQL fixture, a disposable home and real managed
// Victoria binaries. It proves the public request path and export/readback,
// rather than manufacturing reports which bypass the instrumented connector.
func runHarnessObservabilityProbe(parent context.Context, repo string) (proof map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	proof = map[string]any{}
	if !harnessDockerAvailable(ctx) {
		return proof, errors.New("docker unavailable; SQL observability proof did not run")
	}
	base, err := os.MkdirTemp("", "scenery-observability-")
	if err != nil {
		return proof, err
	}
	root, home := filepath.Join(base, "app"), filepath.Join(base, "home")
	p := &worktreeRuntimeProbe{ctx: ctx, repo: repo, root: base, home: home, binary: harnessLocalSceneryBinaryPath(repo), env: harnessAppEnv(home)}
	restore := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(home), "DATABASE_URL": nil})
	defer restore()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()
		_, downErr := p.runWithContext(cleanup, repo, p.binary, "down", "--app-root", root, "-o", "json")
		record, recordErr := p.record(root)
		var databaseErr error
		if recordErr == nil {
			databaseErr = cleanupHarnessWorktreePostgres(cleanup, root, record.AppID)
		} else if !errors.Is(recordErr, os.ErrNotExist) {
			databaseErr = recordErr
		}
		cleanupErr := errors.Join(downErr, databaseErr)
		if cleanupErr == nil {
			proof["cleanup"] = "owned runtime, Victoria processes and PostgreSQL cluster removed"
		}
		resultErr = errors.Join(resultErr, cleanupErr)
		if resultErr == nil {
			resultErr = os.RemoveAll(base)
		}
		if resultErr != nil {
			proof["retained_probe_root"] = base
		}
	}()
	if err := p.prepareGitWorktrees(root, filepath.Join(base, "unused-worktree")); err != nil {
		return proof, err
	}
	if err := prepareObservabilityOperations(root); err != nil {
		return proof, err
	}
	propagated := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case propagated <- r.Header.Get("traceparent"):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	source := strings.ReplaceAll(observabilityProbeSource, "PROBE_UPSTREAM", upstream.URL)
	if err := os.WriteFile(filepath.Join(root, "library/observability.go"), []byte(source), 0600); err != nil {
		return proof, err
	}
	servicePath := filepath.Join(root, "library/service.go")
	service, err := os.ReadFile(servicePath)
	if err != nil {
		return proof, err
	}
	service = bytes.Replace(service, []byte("rows, err := s.database.QueryContext"), []byte("if err := s.observe(ctx); err != nil { return nil, err }; rows, err := s.database.QueryContext"), 1)
	if err := os.WriteFile(servicePath, service, 0600); err != nil {
		return proof, err
	}
	p.env, err = harnessAppEnvWithVictoria(ctx, repo, home)
	if err != nil {
		return proof, err
	}
	if _, err := p.run(root, p.binary, "provider", "lock", "--app-root", root, "-o", "json"); err != nil {
		return proof, err
	}
	for _, target := range []string{"contracts", "typescript_client.public_api"} {
		if _, err := p.run(root, p.binary, "generate", "--target", target, "--app-root", root, "-o", "json"); err != nil {
			return proof, err
		}
	}
	running, err := p.up(root)
	if err != nil {
		return proof, err
	}
	paths, err := localagent.PathsForWorktree(home, root)
	if err != nil {
		return proof, err
	}
	agent := localagent.NewClient(paths.Socket)
	defer agent.CloseIdleConnections()
	var substrate localagent.Substrate
	if err := waitForHarnessCondition(ctx, func() bool {
		candidate, err := agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
		if err != nil || candidate.Status != "ready" || len(candidate.PIDs) != 3 {
			return false
		}
		for name, pid := range candidate.PIDs {
			if candidate.Owners[name].PID != pid || localagent.VerifyOwner(candidate.Owners[name]) != nil {
				return false
			}
		}
		substrate = candidate
		return true
	}); err != nil {
		return proof, fmt.Errorf("victoria readiness: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, worktreeProbeAPI(running)+"/books", nil)
	if err != nil {
		return proof, err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return proof, err
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		return proof, fmt.Errorf("observed request: HTTP %d: %s: %v", response.StatusCode, body, readErr)
	}
	session, err := harnessLiveSession(ctx, home, root)
	if err != nil {
		return proof, err
	}
	if response.Header.Get("X-Scenery-Process-ID") != session.AppPID || response.Header.Get("X-Scenery-Build-Input-Digest") == "" || response.Header.Get("X-Scenery-Service-Process-ID") == "" {
		return proof, errors.New("response lacks current host/service/build identity")
	}
	traceID := response.Header.Get("X-Trace-Id")
	if len(traceID) != 32 {
		return proof, errors.New("response lacks trace identity")
	}
	proof["trace_id"], proof["host_pid"], proof["service_pid"], proof["build_input_digest"] = traceID, session.AppPID, response.Header.Get("X-Scenery-Service-Process-ID"), response.Header.Get("X-Scenery-Build-Input-Digest")
	stack := victoria.FromSubstrate(substrate)
	var spans []*devdash.TraceSummary
	var observationErr error
	readback, stop := context.WithTimeout(ctx, 40*time.Second)
	defer stop()
	if err := waitForHarnessCondition(readback, func() bool {
		observed, queryErr := stack.GetTraceSummaries(readback, session.BaseAppID, traceID)
		if queryErr != nil {
			if readback.Err() == nil {
				observationErr = queryErr
			}
			return false
		}
		spans = observed
		observationErr = verifyObservabilitySpans(spans)
		return observationErr == nil
	}); err != nil {
		proof["observed_spans"] = spans
		return proof, fmt.Errorf("trace readback: %w: %v", err, observationErr)
	}
	proof["spans"] = len(spans)
	select {
	case parent := <-propagated:
		matched := false
		for _, span := range spans {
			if span.Type == "HTTP" && parent == "00-"+traceID+"-"+span.SpanID+"-01" {
				matched = true
			}
		}
		if !matched {
			return proof, errors.New("outgoing HTTP lost its child trace context")
		}
		proof["http_context_propagation"] = true
	default:
		return proof, errors.New("outgoing HTTP did not reach the upstream")
	}
	rpcURL := "ws" + strings.TrimPrefix(strings.TrimRight(running.Session.RouteManifest.BaseURL, "/"), "http") + "/runtime"
	rpc, err := dialRuntimeBounds(ctx, rpcURL)
	if err != nil {
		return proof, err
	}
	defer func() { _ = rpc.conn.Close() }()
	statusResponse, err := rpc.call(1, "status", map[string]any{}, 5*time.Second)
	if err != nil || statusResponse.Error != nil {
		return proof, fmt.Errorf("runtime status: %v %+v", err, statusResponse.Error)
	}
	var runtimeStatus struct {
		AppID string `json:"app_id"`
	}
	if err := json.Unmarshal(statusResponse.Result, &runtimeStatus); err != nil {
		return proof, err
	}
	traceResponse, err := rpc.call(2, "traces/get", map[string]any{"app_id": runtimeStatus.AppID, "trace_id": traceID}, 5*time.Second)
	if err != nil || traceResponse.Error != nil {
		return proof, fmt.Errorf("runtime trace detail: %v %+v", err, traceResponse.Error)
	}
	var traceDetail devdash.TraceDetail
	if err := json.Unmarshal(traceResponse.Result, &traceDetail); err != nil {
		return proof, err
	}
	if len(traceDetail.Spans) != len(spans) || !bytes.Contains(traceResponse.Result, []byte("ProbeNested")) {
		return proof, fmt.Errorf("RPC detail incomplete: %d/%d spans", len(traceDetail.Spans), len(spans))
	}
	proof["trace_detail_rpc"] = "scoped span tree and SQL events verified"
	if err := waitForHarnessCondition(readback, func() bool {
		listResponse, callErr := rpc.call(3, "traces/list", map[string]any{"app_id": runtimeStatus.AppID}, 5*time.Second)
		if callErr != nil || listResponse.Error != nil || !bytes.Contains(listResponse.Result, []byte(traceID)) {
			observationErr = fmt.Errorf("runtime trace list: %v %+v: %s", callErr, listResponse.Error, listResponse.Result)
			return false
		}
		return true
	}); err != nil {
		return proof, fmt.Errorf("trace list readback: %w: %v", err, observationErr)
	}
	missingResponse, err := rpc.call(4, "traces/get", map[string]any{"app_id": runtimeStatus.AppID, "trace_id": strings.Repeat("f", 32)}, 5*time.Second)
	var missingDetail devdash.TraceDetail
	if err != nil || missingResponse.Error != nil || json.Unmarshal(missingResponse.Result, &missingDetail) != nil || missingDetail.Spans == nil || len(missingDetail.Spans) != 0 {
		return proof, fmt.Errorf("unknown trace was not empty: %v %+v: %s", err, missingResponse.Error, missingResponse.Result)
	}
	proof["trace_list_and_missing_rpc"] = true
	if _, err := p.run(root, p.binary, "generate", "--target", "typescript_client.public_api", "--app-root", root, "-o", "json"); err != nil {
		return proof, err
	}
	clientProbe := filepath.Join(root, "trace-client.ts")
	if err := os.WriteFile(clientProbe, []byte(observabilityClientProbe), 0600); err != nil {
		return proof, err
	}
	clientOutput, err := p.run(root, "bun", "run", clientProbe, worktreeProbeAPI(running))
	if err != nil {
		return proof, err
	}
	var clientProof map[string]any
	if err := json.Unmarshal([]byte(clientOutput), &clientProof); err != nil {
		return proof, fmt.Errorf("client proof: %w: %s", err, clientOutput)
	}
	proof["generated_client"] = clientProof

	var workID string
	for _, span := range spans {
		if span.Type == "WORK" {
			workID = span.SpanID
		}
	}
	detailReq, err := http.NewRequestWithContext(ctx, http.MethodGet, stack.BaseURL("traces")+"/select/jaeger/api/traces/"+traceID, nil)
	if err != nil {
		return proof, err
	}
	detail, err := http.DefaultClient.Do(detailReq)
	if err != nil {
		return proof, err
	}
	detailBytes, err := io.ReadAll(detail.Body)
	_ = detail.Body.Close()
	if err != nil {
		return proof, err
	}
	if bytes.Contains(detailBytes, []byte("private-query-value")) || bytes.Contains(detailBytes, []byte("private-argument")) {
		return proof, errors.New("SQL trace exposed a literal or bound argument")
	}
	for _, needle := range []string{"ProbeLiteral", "args_count", "rows_affected", "SQLSTATE 22P02", "http_headers", "http_body", "storage_result"} {
		if !bytes.Contains(detailBytes, []byte(needle)) {
			return proof, fmt.Errorf("trace is missing %s", needle)
		}
	}
	proof["sql_redacted"], proof["outbound_http_events"] = true, true
	// Exercise the supported scoped CLI query paths as well as the substrate
	// detail needed to assert SQL events and parent identifiers.
	if err := waitForHarnessCondition(readback, func() bool {
		output, err := runHarnessAppCLIWithEnv(readback, repo, root, p.env, "logs", "query", "--query", `"observability-probe-log"`, "--since", "5m", "-o", "json")
		if err != nil {
			observationErr = err
			return false
		}
		var logs obs.LogsQueryResult
		if err := decodeCLIJSON(output, &logs); err != nil {
			observationErr = err
			return false
		}
		for _, log := range logs.Logs {
			encoded, _ := json.Marshal(log)
			if log.TraceID == traceID && log.SpanID == workID && bytes.Contains(encoded, []byte("probe.component")) && bytes.Contains(encoded, []byte("probe.attempt.number")) && !bytes.Contains(encoded, []byte("private-log-token")) && logs.Scope.Enforced {
				return true
			}
		}
		observationErr = fmt.Errorf("correlated bound log missing (%d logs)", len(logs.Logs))
		return false
	}); err != nil {
		return proof, fmt.Errorf("log readback: %w: %v", err, observationErr)
	}
	proof["correlated_structured_logs"] = true
	if err := waitForHarnessCondition(readback, func() bool {
		output, err := runHarnessAppCLIWithEnv(readback, repo, root, p.env, "metrics", "query", "--promql", `scenery_request_duration_seconds{scenery_trace_type="DB"}`, "--instant", "-o", "json")
		if err != nil {
			observationErr = err
			return false
		}
		var metrics obs.MetricsQueryResult
		if err := decodeCLIJSON(output, &metrics); err != nil {
			observationErr = err
			return false
		}
		return len(metrics.Series) > 0 && metrics.Scope.Enforced
	}); err != nil {
		return proof, fmt.Errorf("metric readback: %w: %v", err, observationErr)
	}
	proof["sql_duration_metrics"] = true
	output, err := runHarnessAppCLIWithEnv(ctx, repo, root, p.env, "traces", "list", "--trace-id", traceID, "-o", "json")
	if err != nil {
		return proof, err
	}
	if !bytes.Contains(output, []byte(traceID)) {
		return proof, errors.New("CLI trace list did not return the request")
	}
	proof["cli_trace_lookup"] = true
	return proof, nil
}

func verifyObservabilitySpans(spans []*devdash.TraceSummary) error {
	var root, work *devdash.TraceSummary
	for _, span := range spans {
		if span.Type == "REQUEST" {
			root = span
		}
		if span.Type == "WORK" {
			work = span
		}
	}
	if root == nil || work == nil || work.ParentSpanID == nil || *work.ParentSpanID != root.SpanID {
		return errors.New("request and application span linkage missing")
	}
	found := map[string]bool{}
	for _, span := range spans {
		if span.Type != "DB" || span.EndpointName == nil {
			continue
		}
		name := *span.EndpointName
		if strings.HasPrefix(name, "Probe") && name != "ProbeNested" {
			if span.ParentSpanID == nil || *span.ParentSpanID != work.SpanID || span.DurationNanos == 0 {
				return fmt.Errorf("SQL span %s lost parent or duration", name)
			}
			if span.IsError != (name == "ProbeFailure") {
				return fmt.Errorf("SQL span %s has incorrect failure state", name)
			}
			found[name] = true
		}
	}
	for _, name := range []string{"ProbeLiteral", "ProbePrepared", "ProbeTransaction", "ProbeFailure"} {
		if !found[name] {
			return fmt.Errorf("missing SQL span %s", name)
		}
	}
	byID := map[string]*devdash.TraceSummary{}
	kinds := map[string]int{}
	for _, s := range spans {
		byID[s.SpanID] = s
		kinds[s.Type]++
	}
	for _, kind := range []string{"INTERNAL", "DURABLE", "HTTP", "STORAGE"} {
		if kinds[kind] == 0 {
			return fmt.Errorf("missing automatic %s span", kind)
		}
	}
	nestedParents := map[string]bool{}
	for _, s := range spans {
		if s.Type == "DB" && s.EndpointName != nil && *s.EndpointName == "ProbeNested" && s.ParentSpanID != nil {
			if parent := byID[*s.ParentSpanID]; parent != nil {
				nestedParents[parent.Type] = true
			}
		}
	}
	if !nestedParents["INTERNAL"] || !nestedParents["DURABLE"] {
		return errors.New("nested SQL lost internal or durable parent")
	}
	return nil
}

const observabilityProbeSource = `package library
import("context"; "fmt"; "log/slog"; "net/http"; "scenery.sh"; "scenery.sh/db"; "scenery.sh/storage"; "strings"; "io"; contract "example.com/library-desk/library/scenerycontract")
func(s *Service) observe(ctx context.Context) error {
 ctx, span := scenery.StartSpan(ctx,"sql-observability"); defer span.End(nil)
 invocation,ok := scenery.InvocationFromContext(ctx); if !ok { return fmt.Errorf("missing invocation") }
 if _,err := s.input.Clients.Probe.Invoke(ctx,invocation,contract.TraceProbeInput{}); err != nil { return err }
 if _,err := s.input.Clients.Background.Invoke(ctx,invocation,contract.TraceProbeInput{}); err != nil { return err }
 var value string
 if err := s.database.QueryRowContext(ctx,"-- name: ProbeLiteral :one\nSELECT $$private-query-value$$::text").Scan(&value); err != nil { return err }
 pool,err:=db.Get(ctx,"library"); if err!=nil{return err}
 stmt,err:=pool.PrepareContext(ctx,"-- name: ProbePrepared :one\nSELECT $1::text");if err!=nil{return err};defer stmt.Close()
 if err:=stmt.QueryRowContext(ctx,"private-argument").Scan(&value);err!=nil{return err}
 tx,err:=s.database.BeginTx(ctx,nil);if err!=nil{return err};defer tx.Rollback()
 if _,err:=tx.ExecContext(ctx,"-- name: ProbeTransaction :exec\nSELECT 42");err!=nil{return err}
 if err:=tx.Commit();err!=nil{return err}
 if _,err:=s.database.ExecContext(ctx,"-- name: ProbeFailure :exec\nSELECT $1::text::integer","private-argument");err==nil{return fmt.Errorf("expected SQL failure")}
 done:=make(chan struct{});go func(){defer close(done);slog.Default().WithGroup("probe").With("component","sql").WithGroup("attempt").InfoContext(ctx,"observability-probe-log","number",1,"token","private-log-token")}();<-done
 request,err:=http.NewRequestWithContext(ctx,"GET","PROBE_UPSTREAM",nil);if err!=nil{return err}
 response,err:=http.DefaultClient.Do(request);if err!=nil{return err};return response.Body.Close()
}
func(s *Service) TraceProbe(ctx context.Context, _ contract.TraceProbeInput) (contract.TraceProbeOutcome,error) {
 if _,err := s.database.ExecContext(ctx,"-- name: ProbeNested :exec\nSELECT 1"); err != nil { return nil,err }
 store,err := storage.Default(ctx);if err!=nil{return nil,err}
 if _,err = store.Put(ctx,"proof.txt",strings.NewReader("hello"),storage.PutOptions{});err!=nil{return nil,err}
 body,_,err := store.Get(ctx,"proof.txt",storage.GetOptions{});if err!=nil{return nil,err}
 _,err=io.Copy(io.Discard,body);_ = body.Close();if err!=nil{return nil,err}
 if err=store.Delete(ctx,"proof.txt",storage.DeleteOptions{});err!=nil{return nil,err}
 return contract.TraceProbeDone{Value:scenery.Unit{}},nil
}
`

const observabilityClientProbe = `
import { PublicApiClient } from "./client/generated/client.ts";
import { url } from "./client/generated/runtime.ts";
const events: any[] = [];
let attempts = 0, cancelled = false;
const client = new PublicApiClient({
 baseUrl: url(process.argv[2]),
 retryRuntime: { now: Date.now, sleep: async () => {} },
 onTrace(event) { events.push(event); },
 fetch: async (input, init) => {
  attempts++;
  if (attempts === 1) return new Response(new ReadableStream({ cancel() { cancelled = true; } }), { status: 503, headers: { "x-trace-id": "cccccccccccccccccccccccccccccccc" } });
  return fetch(input, init);
 },
});
const result = await client.create({ bookId: "trace-client-proof", title: "proof" });
const responses = events.filter(e => e.phase === "response");
const terminal = events.at(-1);
if (!cancelled || attempts !== 2 || responses.length !== 2 || responses[0].traceId !== "cccccccccccccccccccccccccccccccc" || !/^[a-f0-9]{32}$/.test(terminal.traceId ?? "") || terminal.traceId !== responses[1].traceId || terminal.attempt !== 2 || terminal.outcome !== "success" || result.kind !== "result") throw new Error("generated client lost retry lifecycle or backend trace correlation");
console.log(JSON.stringify({ attempts, trace_id: terminal.traceId, discarded_response_cancelled: cancelled, phases: events.map(e => e.phase) }));
`
