package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	obs "scenery.sh/internal/observability"
	"scenery.sh/internal/schemacheck"
)

// Exercise the genuine public catalog owners through HTTP. Malformed fixtures
// establish decoder behavior, independently of the real Victoria control.
func proveHarnessMetricCatalog(parent context.Context, repo string, artifacts harnessArtifactContext) (proof map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	type fixture struct {
		name, endpoint, data, want string
		limit                      int
	}
	cases := []fixture{
		{"null-label", "series", `[{"private-label":null}]`, "", 1},
		{"mixed-labels", "series", `[{"job":"api","private-label":null}]`, "", 1},
		{"null-label-after-limit", "series", `[{"job":"api"},{"private-label":null}]`, "", 1},
		{"null-row", "series", `[null]`, "", 1},
		{"null-row-after-limit", "series", `[{},null]`, "", 1},
		{"emitted-null-row", "series", `[{},null]`, "", 0},
		{"string-catalog", "series", `["private-label"]`, "", 1},
		{"string-row-after-limit", "series", `[{"job":"api"},"private-row"]`, "", 1},
		{"query-shape", "series", `{"resultType":"vector","result":[]}`, "", 1},
		{"null-data", "series", `null`, "", 1},
		{"missing-data", "series", "", "", 1},
		{"numeric-label", "series", `[{"private-label":7}]`, "", 1},
		{"boolean-label", "series", `[{"private-label":false}]`, "", 1},
		{"object-label", "series", `[{"private-label":{}}]`, "", 1},
		{"array-label", "series", `[{"private-label":[]}]`, "", 1},
		{"empty-array", "series", `[]`, `[]`, 1},
		{"empty-object", "series", `[{}]`, `[{}]`, 1},
		{"empty-label", "series", `[{"job":""}]`, `[{"job":""}]`, 1},
		{"literal-null", "series", `[{"job":" null \t"}]`, `[{"job":" null \t"}]`, 1},
		{"special-string", "series", `[{"job":"č/\\\""}]`, `[{"job":"č/\\\""}]`, 1},
		{"valid-limit", "series", `[{"job":"api"},{"job":"worker"}]`, `[{"job":"api"}]`, 1},
		{"unlimited", "series", `[{"job":"api"},{"job":"worker"}]`, `[{"job":"api"},{"job":"worker"}]`, 0},
		{"labels-null", "labels", `[null]`, "", 1},
		{"labels-after-limit", "labels", `["job",null]`, "", 1},
		{"labels-wrong-shape", "labels", `[{"job":"api"}]`, "", 1},
		{"labels-missing-data", "labels", "", "", 1},
		{"labels-empty", "labels", `[]`, `[]`, 1},
		{"labels-empty-string", "labels", `[""]`, `[""]`, 1},
		{"labels-limit", "labels", `["z","a"]`, `["z"]`, 1},
		{"labels-unlimited", "labels", `["z","a"]`, `["a","z"]`, 0},
	}
	byName := make(map[string]fixture, len(cases))
	for _, item := range cases {
		byName[item.name] = item
	}
	proof = map[string]any{"scope": "actual public MetricsSeries/MetricsLabels HTTP calls with synthetic backend input and caller-supplied scope; not malformed-input CLI exits or Victoria incidence", "pid": os.Getpid()}
	type requestEvidence struct {
		Method      string     `json:"method"`
		Path        string     `json:"path"`
		ContentType string     `json:"content_type"`
		Form        url.Values `json:"form"`
	}
	var mu sync.Mutex
	var requests []requestEvidence
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return proof, err
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		item, ok := byName[r.Form.Get("match[]")]
		mu.Lock()
		requests = append(requests, requestEvidence{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Form: r.Form})
		mu.Unlock()
		if !ok || r.Method != http.MethodPost || r.URL.Path != "/prometheus/api/v1/"+item.endpoint {
			http.Error(w, "unexpected catalog request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body := `{"status":"success"}`
		if item.data != "" {
			body = `{"status":"success","data":` + item.data + `}`
		}
		_, _ = fmt.Fprint(w, body)
	})}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() {
		closeErr := server.Close()
		select {
		case serveErr := <-served:
			if !errors.Is(serveErr, http.ErrServerClosed) {
				closeErr = errors.Join(closeErr, fmt.Errorf("catalog server exit: %w", serveErr))
			}
		case <-time.After(time.Second):
			closeErr = errors.Join(closeErr, errors.New("catalog server did not terminate"))
		}
		if _, acceptErr := listener.Accept(); !errors.Is(acceptErr, net.ErrClosed) {
			closeErr = errors.Join(closeErr, errors.New("catalog listener remains open"))
		}
		proof["listener_closed"], proof["server_terminated"] = closeErr == nil, closeErr == nil
		resultErr = errors.Join(resultErr, closeErr)
		data, marshalErr := json.MarshalIndent(proof, "", "  ")
		if marshalErr == nil {
			artifact, writeErr := artifacts.Write("metric catalog HTTP proof", "observability-metric-catalog.json", "", data)
			proof["artifact"] = artifact
			marshalErr = writeErr
		}
		resultErr = errors.Join(resultErr, marshalErr)
	}()
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	var results []map[string]any
	for _, item := range cases {
		q := obs.MetricsCatalogQuery{BaseURL: "http://" + listener.Addr().String(), Scope: obs.QueryScope{AppID: "catalog-probe", SessionID: "catalog-session", AppRoot: repo, AppRootHash: "catalog-root", Enforced: true}, Bounds: obs.TimeBounds{Start: at.Add(-time.Minute), End: at}, Match: item.name, Limit: item.limit, Timeout: time.Second}
		var collection any
		var result any
		var kind string
		var queryErr error
		if item.endpoint == "series" {
			value, callErr := obs.MetricsSeries(ctx, q)
			collection, result, kind, queryErr = value.Series, value, value.Kind, callErr
		} else {
			value, callErr := obs.MetricsLabels(ctx, q)
			collection, result, kind, queryErr = value.Labels, value, value.Kind, callErr
		}
		errorText := ""
		if queryErr != nil {
			errorText = queryErr.Error()
		}
		results = append(results, map[string]any{"name": item.name, "endpoint": item.endpoint, "data": item.data, "limit": item.limit, "result": result, "error": errorText})
		proof["results"] = results
		if item.want == "" {
			if queryErr == nil || kind != "" || strings.Contains(errorText, "private-") {
				return proof, fmt.Errorf("catalog %s accepted malformed input or exposed private detail", item.name)
			}
			continue
		}
		if queryErr != nil {
			return proof, fmt.Errorf("catalog %s rejected control: %w", item.name, queryErr)
		}
		data, err := json.Marshal(collection)
		if err != nil {
			return proof, err
		}
		var got, want any
		if err := json.Unmarshal(data, &got); err != nil {
			return proof, err
		}
		if err := json.Unmarshal([]byte(item.want), &want); err != nil || !reflect.DeepEqual(got, want) {
			return proof, fmt.Errorf("catalog %s changed valid inventory", item.name)
		}
		if diagnostics := schemacheck.ValidateFile(filepath.Join(repo, "docs/schemas/scenery.metrics."+item.endpoint+".schema.json"), result); len(diagnostics) != 0 {
			return proof, fmt.Errorf("catalog %s schema: %v", item.name, diagnostics)
		}
	}
	mu.Lock()
	actual := requests
	mu.Unlock()
	proof["requests"], proof["case_count"] = actual, len(cases)
	if len(actual) != len(cases) {
		return proof, errors.New("catalog HTTP request inventory is incomplete")
	}
	for index, request := range actual {
		form := request.Form
		if request.Method != http.MethodPost || request.Path != "/prometheus/api/v1/"+cases[index].endpoint || request.ContentType != "application/x-www-form-urlencoded" || len(form["match[]"]) != 1 || form["match[]"][0] != cases[index].name || !reflect.DeepEqual(form["extra_label"], []string{"scenery_app=catalog-probe", "scenery_session_id=catalog-session", "scenery_app_root_hash=catalog-root"}) || !reflect.DeepEqual(form["start"], []string{at.Add(-time.Minute).Format(time.RFC3339Nano)}) || !reflect.DeepEqual(form["end"], []string{at.Format(time.RFC3339Nano)}) {
			return proof, errors.New("catalog HTTP selector/bounds/scope changed")
		}
	}
	return proof, nil
}
