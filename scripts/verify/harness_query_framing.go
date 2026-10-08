package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	obs "scenery.sh/internal/observability"
	"scenery.sh/internal/schemacheck"
)

type queryFramingFixture struct {
	name, mode, body       string
	wantError, short, hold bool
	rows                   int
}

func queryFramingDocument(mode string) string {
	switch mode {
	case "vector":
		return `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1.000000001,"2"]}]}}`
	case "matrix":
		return `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1.000000001,"NaN"],[2,"+Inf"],[3,"-Inf"]]}]}}`
	case "labels":
		return `{"status":"success","data":["job"]}`
	case "series":
		return `{"status":"success","data":[{}]}`
	default:
		return `{"_msg":"owned-framing","n":9007199254740993}`
	}
}

// Genuine public HTTP calls complement in-memory read-cut/Close tests. These
// synthetic responses establish decoder capability, not Victoria incidence or
// malformed native CLI exits. The existing real-backend journey is retained.
func proveHarnessQueryFraming(parent context.Context, repo string, artifacts harnessArtifactContext) (proof map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	fixtures := []queryFramingFixture{}
	for _, mode := range []string{"vector", "matrix", "labels", "series", "logs", "tail"} {
		doc := queryFramingDocument(mode)
		fixtures = append(fixtures, queryFramingFixture{name: mode + "-no-newline", mode: mode, body: doc, rows: 1}, queryFramingFixture{name: mode + "-ASCII-whitespace", mode: mode, body: doc + " \t\r\n", rows: 1})
		suffixes := []struct{ name, value string }{{"object", ` {"private-framing-token":1}`}, {"null", "null"}, {"array", "[]"}, {"garbage", "private-framing-token"}, {"vertical-tab", "\v"}, {"form-feed", "\f"}, {"NBSP", "\u00a0"}, {"em-space", "\u2003"}}
		for _, suffix := range suffixes {
			item := queryFramingFixture{name: mode + "-" + suffix.name, mode: mode, body: doc + suffix.value, wantError: true}
			if mode == "logs" || mode == "tail" {
				item.body = doc + "\n" + doc + suffix.value + "\n" + doc + "\n"
				item.rows = 1
			}
			fixtures = append(fixtures, item)
		}
		fixtures = append(fixtures, queryFramingFixture{name: mode + "-short-body", mode: mode, body: doc, wantError: true, short: true, rows: 1}, queryFramingFixture{name: mode + "-healthy-retry", mode: mode, body: doc, rows: 1})
		if mode == "logs" || mode == "tail" {
			for _, prefix := range []struct{ name, value string }{{"leading-VT", "\v"}, {"leading-NBSP", "\u00a0"}, {"Unicode-blank", "\u2003\n"}} {
				fixtures = append(fixtures, queryFramingFixture{name: mode + "-" + prefix.name, mode: mode, body: prefix.value + doc, wantError: true})
			}
			fixtures = append(fixtures, queryFramingFixture{name: mode + "-ASCII-blank", mode: mode, body: " \t\r\n\n" + doc, rows: 1}, queryFramingFixture{name: mode + "-two-NDJSON", mode: mode, body: doc + "\n" + doc, rows: 2})
		}
	}
	fixtures = append(fixtures, queryFramingFixture{name: "tail-held-cancel", mode: "tail", body: queryFramingDocument("tail") + "\n", hold: true, rows: 1}, queryFramingFixture{name: "tail-held-callback", mode: "tail", body: queryFramingDocument("tail") + "\n", hold: true, rows: 1})
	byName := make(map[string]queryFramingFixture, len(fixtures))
	for _, item := range fixtures {
		byName[item.name] = item
	}
	type requestEvidence struct {
		Name          string     `json:"name"`
		Method        string     `json:"method"`
		Path          string     `json:"path"`
		ContentType   string     `json:"content_type"`
		Form          url.Values `json:"form"`
		BodyBytes     int        `json:"body_bytes"`
		DeclaredBytes int        `json:"declared_bytes"`
		BodySHA256    string     `json:"body_sha256"`
	}
	var mu sync.Mutex
	var requests []requestEvidence
	var handlers sync.WaitGroup
	peers := make(chan string, 2)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		name := r.Form.Get("query")
		if name == "" {
			name = r.Form.Get("match[]")
		}
		item, ok := byName[name]
		if !ok {
			http.Error(w, "unknown fixture", 400)
			return
		}
		declared := len(item.body)
		if item.short {
			declared += 9
		}
		mu.Lock()
		requests = append(requests, requestEvidence{Name: name, Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Form: r.Form, BodyBytes: len(item.body), DeclaredBytes: declared, BodySHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(item.body)))})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if item.hold {
			_, _ = fmt.Fprint(w, item.body)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				peers <- name
			case <-ctx.Done():
			}
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(declared))
		_, _ = fmt.Fprint(w, item.body)
	})}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	proof = map[string]any{"scope": "all six public query modes with synthetic HTTP framing/transport fixtures; no native malformed CLI or total-memory/production-incidence certification"}
	defer func() {
		closeErr := server.Close()
		select {
		case e := <-served:
			if !errors.Is(e, http.ErrServerClosed) {
				closeErr = errors.Join(closeErr, e)
			}
		case <-time.After(time.Second):
			closeErr = errors.Join(closeErr, errors.New("framing server did not terminate"))
		}
		joined := make(chan struct{})
		go func() { handlers.Wait(); close(joined) }()
		select {
		case <-joined:
			proof["all_handlers_joined"] = true
		case <-time.After(time.Second):
			closeErr = errors.Join(closeErr, errors.New("framing handlers did not terminate"))
		}
		if _, e := listener.Accept(); !errors.Is(e, net.ErrClosed) {
			closeErr = errors.Join(closeErr, errors.New("framing listener remains open"))
		}
		proof["listener_closed"], proof["server_joined"] = closeErr == nil, closeErr == nil
		mu.Lock()
		proof["requests"] = requests
		mu.Unlock()
		resultErr = errors.Join(resultErr, closeErr)
		data, e := json.MarshalIndent(proof, "", "  ")
		if e == nil {
			artifact, writeErr := artifacts.Write("query framing HTTP proof", "observability-query-framing.json", "", data)
			e = writeErr
			proof["artifact"] = artifact
		}
		resultErr = errors.Join(resultErr, e)
	}()
	base := "http://" + listener.Addr().String()
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	scope := obs.QueryScope{AppID: "framing-probe", SessionID: "framing-session", AppRoot: repo, AppRootHash: "framing-root", Enforced: true}
	bounds := obs.TimeBounds{Since: "15m", Start: at.Add(-15 * time.Minute), End: at}
	var results []map[string]any
	for _, item := range fixtures {
		child, stop := context.WithCancel(ctx)
		var value any
		var kind, schema string
		var queryErr error
		var entries []obs.LogsTailEntry
		callbackErr := errors.New("owned framing callback failure")
		switch item.mode {
		case "vector", "matrix":
			r, e := obs.QueryMetrics(child, obs.MetricsQuery{BaseURL: base, Scope: scope, Bounds: bounds, PromQL: item.name, Instant: item.mode == "vector", Step: 5 * time.Second, Timeout: time.Second, Limit: 1})
			value, kind, schema, queryErr = r, r.Kind, "scenery.metrics.query", e
			if e == nil {
				queryErr = validateFramingMetricControl(r, item.mode)
			}
		case "labels", "series":
			q := obs.MetricsCatalogQuery{BaseURL: base, Scope: scope, Bounds: bounds, Match: item.name, Timeout: time.Second, Limit: 1}
			if item.mode == "labels" {
				r, e := obs.MetricsLabels(child, q)
				value, kind, schema, queryErr = r, r.Kind, "scenery.metrics.labels", e
				if e == nil && !reflect.DeepEqual(r.Labels, []string{"job"}) {
					queryErr = errors.New("framing labels changed")
				}
			} else {
				r, e := obs.MetricsSeries(child, q)
				value, kind, schema, queryErr = r, r.Kind, "scenery.metrics.series", e
				if e == nil && (len(r.Series) != 1 || len(r.Series[0]) != 0) {
					queryErr = errors.New("framing series changed")
				}
			}
		case "logs":
			r, e := obs.QueryLogs(child, obs.LogsQuery{BaseURL: base, Scope: scope, Bounds: bounds, Query: item.name, Timeout: time.Second, Limit: 25})
			value, kind, schema, queryErr = r, r.Kind, "scenery.logs.query", e
			if e == nil {
				if len(r.Logs) != item.rows {
					queryErr = errors.New("framing log inventory changed")
				}
				for _, entry := range r.Logs {
					queryErr = errors.Join(queryErr, validateFramingLogControl(entry))
				}
			}
		case "tail":
			queryErr = obs.TailLogs(child, obs.LogsQuery{BaseURL: base, Scope: scope, Bounds: bounds, Query: item.name, Timeout: time.Second}, func(entry obs.LogsTailEntry) error {
				entries = append(entries, entry)
				if e := validateFramingLogControl(entry.Log); e != nil {
					return e
				}
				if ds := schemacheck.ValidateFile(filepath.Join(repo, "docs/schemas/scenery.logs.tail.entry.schema.json"), entry); len(ds) > 0 {
					return fmt.Errorf("framing tail schema: %v", ds)
				}
				if item.name == "tail-held-cancel" {
					stop()
				}
				if item.name == "tail-held-callback" {
					return callbackErr
				}
				return nil
			})
			value = entries
		}
		stop()
		errorText := ""
		if queryErr != nil {
			errorText = queryErr.Error()
		}
		results = append(results, map[string]any{"name": item.name, "mode": item.mode, "result": value, "error": errorText, "tail_emissions": len(entries)})
		proof["results"] = results
		if item.hold {
			wantErr := error(context.Canceled)
			if item.name == "tail-held-callback" {
				wantErr = callbackErr
			}
			if !errors.Is(queryErr, wantErr) || len(entries) != 1 {
				return proof, fmt.Errorf("held tail %s failed prompt/callback proof: %w", item.name, queryErr)
			}
			select {
			case peer := <-peers:
				if peer != item.name {
					return proof, errors.New("tail peer identity changed")
				}
			case <-time.After(3 * time.Second):
				return proof, errors.New("tail peer context did not terminate")
			}
			results[len(results)-1]["emitted_before_connection_eof"] = true
			results[len(results)-1]["peer_request_context_terminated"] = true
		} else {
			if (queryErr != nil) != item.wantError || strings.Contains(errorText, "private-framing-token") {
				return proof, fmt.Errorf("framing %s error outcome wrong: %w", item.name, queryErr)
			}
			if item.mode == "tail" && len(entries) != item.rows {
				return proof, fmt.Errorf("framing %s emitted %d rows, want%d", item.name, len(entries), item.rows)
			}
			if item.mode != "tail" {
				if item.wantError && kind != "" {
					return proof, errors.New("malformed framing returned a successful envelope")
				}
				if !item.wantError {
					if ds := schemacheck.ValidateFile(filepath.Join(repo, "docs/schemas/"+schema+".schema.json"), value); len(ds) > 0 {
						return proof, fmt.Errorf("framing %s schema: %v", item.name, ds)
					}
				}
			}
		}
	}
	mu.Lock()
	actual := append([]requestEvidence(nil), requests...)
	mu.Unlock()
	if len(actual) != len(fixtures) {
		return proof, errors.New("framing HTTP inventory incomplete")
	}
	for i, r := range actual {
		item := fixtures[i]
		path, form := framingExpectedRequest(item, bounds, scope)
		if r.Name != item.name || r.Method != http.MethodPost || r.Path != path || r.ContentType != "application/x-www-form-urlencoded" || !reflect.DeepEqual(r.Form, form) {
			return proof, fmt.Errorf("framing %s request/scope changed", item.name)
		}
	}
	proof["case_count"] = len(fixtures)
	proof["checked_request_inventory"] = true
	return proof, nil
}

func validateFramingLogControl(entry obs.LogEntry) error {
	n, ok := entry.Raw["n"].(json.Number)
	if !ok || n.String() != "9007199254740993" || entry.Message != "owned-framing" {
		return errors.New("framing log normalization changed")
	}
	return nil
}
func validateFramingMetricControl(r obs.MetricsQueryResult, mode string) error {
	if r.ResultType != mode || len(r.Series) != 1 {
		return errors.New("framing metric inventory changed")
	}
	at := time.Unix(1, 1).UTC().Format(time.RFC3339Nano)
	if mode == "vector" {
		v := r.Series[0].Value
		if v == nil || v.Time != at || v.Value != "2" {
			return errors.New("framing vector exact sample changed")
		}
	} else {
		values := r.Series[0].Values
		if len(values) != 3 || values[0].Time != at || values[0].Value != "NaN" || values[1].Value != "+Inf" || values[2].Value != "-Inf" {
			return errors.New("framing matrix special samples changed")
		}
	}
	return nil
}
func framingExpectedRequest(item queryFramingFixture, bounds obs.TimeBounds, scope obs.QueryScope) (string, url.Values) {
	form := url.Values{"timeout": {"1s"}}
	start, end := bounds.Start.Format(time.RFC3339Nano), bounds.End.Format(time.RFC3339Nano)
	if item.mode == "logs" || item.mode == "tail" {
		form.Set("query", item.name)
		form["extra_filters"] = []string{`(scenery.application_id:"framing-probe" OR scenery_app_id:"framing-probe")`, `(scenery.session_id:"framing-session" OR scenery_session_id:"framing-session")`}
		if item.mode == "tail" {
			form.Set("start_offset", "15m")
			return "/select/logsql/tail", form
		}
		form.Set("start", start)
		form.Set("end", end)
		form.Set("limit", "25")
		return "/select/logsql/query", form
	}
	form["extra_label"] = []string{"scenery_app=" + scope.AppID, "scenery_session_id=" + scope.SessionID, "scenery_app_root_hash=" + scope.AppRootHash}
	if item.mode == "vector" {
		form.Set("query", item.name)
		form.Set("time", end)
		return "/prometheus/api/v1/query", form
	}
	form.Set("start", start)
	form.Set("end", end)
	if item.mode == "matrix" {
		form.Set("query", item.name)
		form.Set("step", "5s")
		return "/prometheus/api/v1/query_range", form
	}
	form.Del("timeout")
	form.Set("match[]", item.name)
	return "/prometheus/api/v1/" + item.mode, form
}
