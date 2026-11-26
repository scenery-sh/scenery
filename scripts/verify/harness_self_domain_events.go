package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
	"scenery.sh/internal/victoria"
)

func runHarnessDomainEventsStep(ctx context.Context, repo string) harnessStep {
	start := time.Now()
	step := harnessStep{Name: "polyglot domain events and durable delivery"}
	var err error
	step.Summary, err = verifyDomainEvents(ctx, repo)
	step.OK, step.DurationMS = err == nil, time.Since(start).Milliseconds()
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func verifyDomainEvents(parent context.Context, repo string) (proof map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-domain-events-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr == nil {
			_ = os.RemoveAll(root)
		}
	}()
	app, home := filepath.Join(root, "app"), filepath.Join(root, "agent")
	proof = map[string]any{"fixture_root": app}
	if err := copyHarnessPolyglotFixture(ctx, repo, app); err != nil {
		return proof, err
	}
	// The probe exercises worker ingress and leaves the fixture's Vite
	// frontend for the frontend probes.
	if err := writeHarnessFixtureConfig(app, map[string]any{
		"name": "polyglot", "auth": map[string]bool{"enabled": true},
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
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, downErr := runHarnessAppCLIWithEnv(cleanup, repo, app, env, "down", "-o", "json")
		returnErr = errors.Join(returnErr, downErr)
		if record, err := paths.LoadRecord("polyglot"); err == nil && record.Postgres != nil {
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
	record, err := paths.LoadRecord("polyglot")
	if err != nil || record.Postgres == nil {
		return proof, fmt.Errorf("fixture Postgres allocation missing: %v", err)
	}
	database := postgresname.DatabaseNameFor(record.AppID, paths.AppRoot)
	db, err := postgresdb.Open(ctx, worktreePostgresURL(record.Postgres, database))
	if err != nil {
		return proof, err
	}
	defer func() { _ = db.Close() }()
	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	endpoint := worktreeProbeAPI(started) + "/api/orders"
	const trace = "1234567890abcdef1234567890abcdef"
	emit := func(id, amount string, status int) (contract.EventReceipt, error) {
		return publishHarnessOrder(ctx, client, endpoint, id, amount, trace, status, proof)
	}
	first, err := emit("example", "9007199254740993", http.StatusOK)
	if err != nil {
		return proof, err
	}
	if first.EventID == "" || first.Event != "orders.accepted" || first.Deliveries != 3 || first.Deduplicated {
		return proof, fmt.Errorf("unexpected first receipt %+v", first)
	}
	duplicate, err := emit("example", "7", http.StatusOK)
	if err != nil {
		return proof, err
	}
	want := first
	want.Deduplicated = true
	if duplicate != want {
		return proof, fmt.Errorf("unexpected duplicate receipt %+v, want %+v", duplicate, want)
	}
	if _, err := emit("denied", "9007199254740993", http.StatusForbidden); err != nil {
		return proof, err
	}
	if err := verifyOrderDelivery(ctx, db, first.EventID, trace); err != nil {
		return proof, err
	}
	proof["receipt"], proof["duplicate"], proof["trace_id"] = first, duplicate, trace
	proof["subscribers"] = []string{"native/go", "pricing/rust", "typescript/ts"}
	proof["denied_status"] = http.StatusForbidden
	if proof["X-App-Host-Pid"] != started.Session.AppPID {
		return proof, fmt.Errorf("host identity differs from the serving session")
	}
	span, err := verifyOrderTrace(ctx, paths.Socket, started.Session.BaseAppID, trace)
	if err != nil {
		return proof, err
	}
	proof["emit_span_id"] = span
	return proof, nil
}

func publishHarnessOrder(ctx context.Context, client *http.Client, endpoint, id, amount, trace string, wantStatus int, proof map[string]any) (contract.EventReceipt, error) {
	var receipt contract.EventReceipt
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(fmt.Sprintf(`{"id":%q,"amount":%q}`, id, amount)))
	if err != nil {
		return receipt, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", "00-"+trace+"-1234567890abcdef-01")
	response, err := client.Do(req)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return receipt, err
	}
	if response.StatusCode != wantStatus {
		return receipt, fmt.Errorf("publish %q returned HTTP %d, want %d: %s", id, response.StatusCode, wantStatus, raw)
	}
	if wantStatus == http.StatusForbidden {
		var problem struct{ Code string }
		if err := json.Unmarshal(raw, &problem); err != nil || problem.Code != "order_denied" {
			return receipt, fmt.Errorf("unexpected denial: %s", raw)
		}
		return receipt, nil
	}
	for _, header := range []string{"X-App-Host-Pid", "X-App-Worker-Pid", "X-App-Generation", "X-App-Build-Digest"} {
		value := response.Header.Get(header)
		if value == "" {
			return receipt, fmt.Errorf("missing served identity header %s", header)
		}
		proof[header] = value
	}
	return receipt, json.Unmarshal(raw, &receipt)
}

func verifyOrderDelivery(ctx context.Context, db *sql.DB, eventID, trace string) error {
	readback, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var count int
	var queryErr error
	err := waitForHarnessCondition(readback, func() bool {
		queryErr = db.QueryRowContext(readback, `SELECT count(*) FROM scenery.durable_jobs
			WHERE memo_json->>'event_id' = $1 AND state = 'succeeded' AND attempt = 1
			AND memo_json->>'trace_id' = $2 AND memo_json->>'event_depth' = '1'
			AND memo_json->>'caller_binding' = service || '/subscription/receive_order'
			AND convert_from(result_blob, 'UTF8')::jsonb->>'value' = $2`, eventID, trace).Scan(&count)
		return queryErr == nil && count == 3
	})
	if err != nil {
		return fmt.Errorf("read back durable executions (%d/3): %w: %v", count, err, queryErr)
	}
	var correct bool
	err = db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM scenery.domain_events) = 3 AND
		(SELECT count(*) FROM scenery.domain_events WHERE id = $1 AND delivery_count = 3
			AND event_name = 'orders.accepted' AND publisher_service = 'consumer/service/consumer'
			AND metadata_json->>'trace_id' = $2
			AND convert_from(payload_blob, 'UTF8')::jsonb->>'id' = 'example') = 1 AND
		(SELECT count(DISTINCT publisher_service) FROM scenery.domain_events WHERE event_name = 'orders.delivered'
			AND delivery_count = 0 AND metadata_json->>'trace_id' = $2
			AND metadata_json->>'event_depth' = '1'
			AND convert_from(payload_blob, 'UTF8')::jsonb->>'id' = 'example') = 2 AND
		(SELECT count(*) FROM scenery.durable_jobs WHERE memo_json ? 'event_id') = 3 AND
		(SELECT count(*) FROM scenery.durable_job_events AS e JOIN scenery.durable_jobs AS j
			ON e.service = j.service AND e.job_id = j.id
			WHERE j.memo_json ? 'event_id' AND e.event_type = 'job.created') = 3`, eventID, trace).Scan(&correct)
	if err != nil || !correct {
		return fmt.Errorf("event, deduplication, authorization or cross-language publication rows differ: %v", err)
	}
	return nil
}

func verifyOrderTrace(ctx context.Context, socket, appID, trace string) (string, error) {
	agent := localagent.NewClient(socket)
	defer agent.CloseIdleConnections()
	substrate, err := agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return "", err
	}
	stack := victoria.FromSubstrate(substrate)
	if stack == nil {
		return "", errors.New("fixture VictoriaTraces is unavailable")
	}
	// The trace index may take about half a minute after ingestion to make
	// a trace searchable by ID.
	readback, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	var lastErr error
	for {
		spans, err := stack.GetTraceSummaries(readback, appID, trace)
		if err != nil && readback.Err() == nil {
			lastErr = err
		}
		for _, span := range spans {
			if span.ServiceName != "consumer" || span.EndpointName == nil || *span.EndpointName != "native/event/accepted" || span.IsError {
				continue
			}
			runtimes := map[string]bool{}
			for _, child := range spans {
				if child.Type == "DURABLE" && child.ParentSpanID != nil && *child.ParentSpanID == span.SpanID && !child.IsError {
					runtimes[child.Runtime] = true
				}
			}
			if runtimes["go"] && runtimes["rust"] && runtimes["ts"] {
				return span.SpanID, nil
			}
		}
		select {
		case <-readback.Done():
			return "", fmt.Errorf("read back emit span and three durable child spans: got %d spans: %w: %v", len(spans), readback.Err(), lastErr)
		case <-poll.C:
		}
	}
}
