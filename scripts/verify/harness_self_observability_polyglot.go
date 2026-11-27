package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	obs "scenery.sh/internal/observability"
	"scenery.sh/internal/victoria"
)

func runHarnessPolyglotObservabilityProbeStep(ctx context.Context, repo string) harnessStep {
	started := time.Now()
	proof, err := runHarnessPolyglotObservabilityProbe(ctx, repo)
	step := harnessStep{
		Name:       "polyglot observability round trip (go, rust, ts, python)",
		Summary:    proof,
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

// runHarnessPolyglotObservabilityProbe exercises traces, logs, and metrics
// across Go, Rust, TypeScript, and Python workers in one running application
// backed by real VictoriaTraces, VictoriaLogs, and VictoriaMetrics.
func runHarnessPolyglotObservabilityProbe(parent context.Context, repo string) (proof map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	proof = map[string]any{}
	if !harnessDockerAvailable(ctx) {
		return proof, errors.New("docker unavailable; polyglot observability proof did not run")
	}
	root, err := os.MkdirTemp("", "scenery-polyglot-obs-")
	if err != nil {
		return proof, err
	}
	defer func() {
		if returnErr == nil {
			_ = os.RemoveAll(root)
		}
	}()
	app, home := filepath.Join(root, "app"), filepath.Join(root, "agent")
	proof["fixture_root"] = app
	if err := copyHarnessPolyglotFixture(ctx, repo, app); err != nil {
		return proof, err
	}
	if err := instrumentPolyglotObservabilityFixture(repo, app); err != nil {
		return proof, err
	}
	if err := writeHarnessFixtureConfig(app, map[string]any{
		"name": "polyglot",
		"auth": map[string]bool{"enabled": true},
		"envs": map[string]any{"local": map[string]bool{"default": true}},
	}); err != nil {
		return proof, err
	}
	env, err := harnessAppEnvWithVictoria(ctx, repo, home)
	if err != nil {
		return proof, err
	}
	if _, err := runHarnessAppCLIWithEnv(ctx, repo, app, env, "generate", "-o", "json"); err != nil {
		return proof, err
	}
	paths, err := localagent.PathsForWorktree(home, app)
	if err != nil {
		return proof, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		_, downErr := runHarnessAppCLIWithEnv(cleanup, repo, app, env, "down", "-o", "json")
		returnErr = errors.Join(returnErr, downErr)
		if record, loadErr := paths.LoadRecord("polyglot"); loadErr == nil && record.Postgres != nil {
			_, pruneErr := runHarnessAppCLIWithEnv(cleanup, repo, app, env, "prune", "--db", "--older-than", "1ns", "-o", "json")
			returnErr = errors.Join(returnErr, pruneErr)
		}
	}()
	output, err := runHarnessAppCLIWithEnv(ctx, repo, app, env, "up", "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return proof, err
	}
	var started detachedDevResult
	if err := decodeCLIJSON(output, &started); err != nil {
		return proof, err
	}
	agent := localagent.NewClient(paths.Socket)
	defer agent.CloseIdleConnections()
	var substrate localagent.Substrate
	if err := waitForHarnessCondition(ctx, func() bool {
		candidate, getErr := agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
		if getErr != nil || candidate.Status != "ready" || len(candidate.PIDs) != 3 {
			return false
		}
		substrate = candidate
		return true
	}); err != nil {
		return proof, fmt.Errorf("victoria readiness: %w", err)
	}
	stack := victoria.FromSubstrate(substrate)
	if stack == nil {
		return proof, errors.New("polyglot Victoria stack unavailable")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	apiBase := worktreeProbeAPI(started)

	// 1. Python -> Rust -> Go (SQL + WORK + log + Victoria URL assertion).
	pyTraceID, err := invokePolyglotJSON(ctx, client, apiBase+"/api/py/quote", `{"amount":"10","note":"rust"}`)
	if err != nil {
		return proof, fmt.Errorf("python->rust->go call: %w", err)
	}
	// 2. TypeScript -> Rust -> Go (SQL + WORK + log + Victoria URL assertion).
	tsTraceID, err := invokePolyglotJSON(ctx, client, apiBase+"/api/ts/quote", `{"amount":"20","note":"rust"}`)
	if err != nil {
		return proof, fmt.Errorf("ts->rust->go call: %w", err)
	}
	// 3. Go event publish -> PUBLISH span + durable Go/Rust/TS subscribers.
	evtTraceID, err := invokePolyglotJSON(ctx, client, apiBase+"/api/orders", `{"id":"polyglot-obs-order","amount":"9007199254740993"}`)
	if err != nil {
		return proof, fmt.Errorf("domain event publish: %w", err)
	}
	proof["trace_ids"] = map[string]string{
		"python_rust_go": pyTraceID,
		"ts_rust_go":     tsTraceID,
		"domain_event":   evtTraceID,
	}

	readback, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	var lastErr error

	// Verify distributed traces across all four languages in VictoriaTraces.
	if err := waitForHarnessCondition(readback, func() bool {
		if err := verifyPolyglotChainTrace(readback, stack, started.Session.BaseAppID, pyTraceID, "/api/py/quote", []string{"python", "rust", "go"}, []string{"py-polyglot-work", "rust-polyglot-work", "go-polyglot-work"}); err != nil {
			lastErr = err
			return false
		}
		if err := verifyPolyglotChainTrace(readback, stack, started.Session.BaseAppID, tsTraceID, "/api/ts/quote", []string{"ts", "rust", "go"}, []string{"ts-polyglot-work", "rust-polyglot-work", "go-polyglot-work"}); err != nil {
			lastErr = err
			return false
		}
		if err := verifyPolyglotPublishTrace(readback, stack, started.Session.BaseAppID, evtTraceID); err != nil {
			lastErr = err
			return false
		}
		return true
	}); err != nil {
		return proof, fmt.Errorf("polyglot traces readback: %w: %v", err, lastErr)
	}
	proof["traces_verified"] = true

	// Verify correlated logs with preserved warn level and attributes across all 4 languages.
	expectedLogs := []struct {
		message string
		lang    string
		traceID string
	}{
		{"polyglot-python-log", "python", pyTraceID},
		{"polyglot-ts-log", "ts", tsTraceID},
		{"polyglot-rust-log", "rust", pyTraceID},
		{"polyglot-go-log", "go", pyTraceID},
	}
	if err := waitForHarnessCondition(readback, func() bool {
		for _, want := range expectedLogs {
			out, qErr := runHarnessAppCLIWithEnv(readback, repo, app, env, "logs", "query", "--query", fmt.Sprintf("%q", want.message), "--since", "5m", "-o", "json")
			if qErr != nil {
				lastErr = qErr
				return false
			}
			var logs obs.LogsQueryResult
			if err := decodeCLIJSON(out, &logs); err != nil {
				lastErr = err
				return false
			}
			matched := slices.ContainsFunc(logs.Logs, func(entry obs.LogEntry) bool {
				encoded, _ := json.Marshal(entry)
				return entry.TraceID == want.traceID &&
					entry.SpanID != "" &&
					entry.Level == "warn" &&
					logs.Scope.Enforced &&
					entry.Raw["runtime"] == want.lang &&
					bytes.Contains(encoded, []byte(`"scenery.log.lang":"`+want.lang+`"`))
			})
			if !matched {
				lastErr = fmt.Errorf("missing correlated warn log %q for %s (got %d rows)", want.message, want.lang, len(logs.Logs))
				return false
			}
		}
		return true
	}); err != nil {
		return proof, fmt.Errorf("polyglot logs readback: %w: %v", err, lastErr)
	}
	proof["logs_verified"] = true

	// Verify latency metrics for all 4 runtimes + custom worker metrics in VictoriaMetrics.
	promQueries := []string{
		`scenery_request_duration_seconds{runtime="go"}`,
		`scenery_request_duration_seconds{runtime="rust"}`,
		`scenery_request_duration_seconds{runtime="ts"}`,
		`scenery_request_duration_seconds{runtime="python"}`,
		`polyglot_rust_quotes{runtime="rust"}`,
		`polyglot_ts_quotes{runtime="ts"}`,
		`polyglot_python_quotes{runtime="python"}`,
	}
	if err := waitForHarnessCondition(readback, func() bool {
		for _, promQL := range promQueries {
			out, qErr := runHarnessAppCLIWithEnv(readback, repo, app, env, "metrics", "query", "--promql", promQL, "--instant", "-o", "json")
			if qErr != nil {
				lastErr = qErr
				return false
			}
			var metrics obs.MetricsQueryResult
			if err := decodeCLIJSON(out, &metrics); err != nil {
				lastErr = err
				return false
			}
			if len(metrics.Series) == 0 || !metrics.Scope.Enforced {
				lastErr = fmt.Errorf("metric query %s returned %d series (enforced=%v)", promQL, len(metrics.Series), metrics.Scope.Enforced)
				return false
			}
		}
		return true
	}); err != nil {
		return proof, fmt.Errorf("polyglot metrics readback: %w: %v", err, lastErr)
	}
	proof["metrics_verified"] = true
	return proof, nil
}

func invokePolyglotJSON(ctx context.Context, client *http.Client, endpoint, payload string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	traceID := resp.Header.Get("X-Trace-Id")
	if len(traceID) != 32 {
		return "", fmt.Errorf("missing X-Trace-Id header on %s", endpoint)
	}
	return traceID, nil
}

func verifyPolyglotChainTrace(ctx context.Context, stack *victoria.Stack, appID, traceID, wantPath string, wantRuntimes, wantWorkSpans []string) error {
	spans, err := stack.GetTraceSummaries(ctx, appID, traceID)
	if err != nil {
		return err
	}
	var root *devdash.TraceSummary
	runtimes := map[string]bool{}
	workSpans := map[string]string{}
	hasDB := false
	for _, span := range spans {
		if span.Type == "REQUEST" && span.IsRoot {
			root = span
		}
		if span.Runtime != "" {
			runtimes[span.Runtime] = true
		}
		if span.Type == "WORK" && span.EndpointName != nil {
			workSpans[*span.EndpointName] = span.Runtime
		}
		if span.Type == "DB" && span.EndpointName != nil && *span.EndpointName == "PolyglotGoQuery" {
			hasDB = true
		}
	}
	if root == nil {
		return fmt.Errorf("trace %s missing root REQUEST span", traceID)
	}
	if root.Generation == 0 {
		return fmt.Errorf("trace %s root span missing scenery.generation", traceID)
	}
	for _, runtime := range wantRuntimes {
		if !runtimes[runtime] {
			return fmt.Errorf("trace %s missing runtime %q (got %v)", traceID, runtime, runtimes)
		}
	}
	for _, name := range wantWorkSpans {
		if _, ok := workSpans[name]; !ok {
			return fmt.Errorf("trace %s missing WORK span %q (got %v)", traceID, name, workSpans)
		}
	}
	if !hasDB {
		return fmt.Errorf("trace %s missing Go SQL DB span PolyglotGoQuery", traceID)
	}
	// Verify raw Jaeger trace carries HTTP request metadata and span_start/span_end events.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stack.BaseURL("traces")+"/select/jaeger/api/traces/"+traceID, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jaeger trace fetch HTTP %d: %v", resp.StatusCode, readErr)
	}
	for _, needle := range []string{"http.request.method", "url.path", wantPath, "http.response.status_code", "span_start", "span_end"} {
		if !bytes.Contains(raw, []byte(needle)) {
			return fmt.Errorf("trace %s missing HTTP metadata %q", traceID, needle)
		}
	}
	return nil
}

func verifyPolyglotPublishTrace(ctx context.Context, stack *victoria.Stack, appID, traceID string) error {
	spans, err := stack.GetTraceSummaries(ctx, appID, traceID)
	if err != nil {
		return err
	}
	var publishSpanID string
	for _, span := range spans {
		if span.Type == "PUBLISH" && span.EndpointName != nil && *span.EndpointName == "native/event/accepted" && !span.IsError {
			publishSpanID = span.SpanID
			break
		}
	}
	if publishSpanID == "" {
		return fmt.Errorf("trace %s missing PUBLISH span for native/event/accepted", traceID)
	}
	durableRuntimes := map[string]bool{}
	for _, span := range spans {
		if span.Type == "DURABLE" && span.ParentSpanID != nil && *span.ParentSpanID == publishSpanID && !span.IsError {
			durableRuntimes[span.Runtime] = true
		}
	}
	if !durableRuntimes["go"] || !durableRuntimes["rust"] || !durableRuntimes["ts"] {
		return fmt.Errorf("trace %s missing durable subscriber runtimes (got %v)", traceID, durableRuntimes)
	}
	return nil
}

func instrumentPolyglotObservabilityFixture(repo, app string) error {
	goLabelSnippet, err := os.ReadFile(filepath.Join(repo, "scripts", "verify", "testdata", "observability", "polyglot_go_label.go.txt"))
	if err != nil {
		return err
	}
	if err := replaceInFileOnce(
		filepath.Join(app, "native/service.go"),
		"\t\"io\"\n",
		"\t\"io\"\n\t\"log/slog\"\n\t\"os\"\n",
	); err != nil {
		return err
	}
	if err := replaceInFileOnce(
		filepath.Join(app, "native/service.go"),
		"func (*Service) Label(ctx context.Context, _ contract.LabelInput) (contract.LabelOutcome, error) {\n",
		string(goLabelSnippet),
	); err != nil {
		return err
	}
	if err := replaceInFileOnce(
		filepath.Join(app, "pricing/src/lib.rs"),
		"        if input.amount.0 < 0 {\n",
		`        context
            .telemetry(&[
                sdk::TelemetryRecord {
                    kind: "span",
                    name: "rust-polyglot-work",
                    message: "rust work",
                    elapsed_ns: 1_000_000,
                    attributes: std::collections::BTreeMap::new(),
                },
                sdk::TelemetryRecord {
                    kind: "log",
                    name: "rust.log",
                    message: "polyglot-rust-log",
                    elapsed_ns: 0,
                    attributes: std::collections::BTreeMap::from([
                        ("level".into(), sdk::serde_json::json!("warn")),
                        ("lang".into(), sdk::serde_json::json!("rust")),
                    ]),
                },
                sdk::TelemetryRecord {
                    kind: "metric",
                    name: "polyglot_rust_quotes",
                    message: "",
                    elapsed_ns: 0,
                    attributes: std::collections::BTreeMap::from([
                        ("value".into(), sdk::serde_json::json!(1.0)),
                        ("unit".into(), sdk::serde_json::json!("1")),
                    ]),
                },
            ])
            .await?;
        if input.amount.0 < 0 {
`,
	); err != nil {
		return err
	}
	if err := replaceInFileOnce(
		filepath.Join(app, "typescript/service.ts"),
		"      if (input.amount < 0n) {\n",
		`      await context.telemetry([
        { kind: "span", name: "ts-polyglot-work", message: "ts work", elapsed_ns: 1_000_000 },
        { kind: "log", name: "ts.log", message: "polyglot-ts-log", attributes: { level: "warn", lang: "ts" } },
        { kind: "metric", name: "polyglot_ts_quotes", attributes: { value: 2, unit: "1" } },
      ]);
      if (input.amount < 0n) {
`,
	); err != nil {
		return err
	}
	if err := replaceInFileOnce(
		filepath.Join(app, "python/service.py"),
		"        if input.amount < 0:\n",
		`        await ctx.telemetry([
            {"kind": "span", "name": "py-polyglot-work", "message": "py work", "elapsed_ns": 1_000_000},
            {"kind": "log", "name": "py.log", "message": "polyglot-python-log", "attributes": {"level": "warn", "lang": "python"}},
            {"kind": "metric", "name": "polyglot_python_quotes", "attributes": {"value": 3, "unit": "1"}},
        ])
        if input.amount < 0:
`,
	); err != nil {
		return err
	}
	return nil
}

func replaceInFileOnce(path, oldText, newText string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(raw, []byte(oldText)) {
		return fmt.Errorf("%s did not contain expected anchor %q", path, oldText)
	}
	updated := bytes.Replace(raw, []byte(oldText), []byte(newText), 1)
	return os.WriteFile(path, updated, 0o600)
}
