package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type responseReadStep struct {
	data string
	err  error
}
type responseTestBody struct {
	steps  []responseReadStep
	closes int
}

func (b *responseTestBody) Read(p []byte) (int, error) {
	if len(b.steps) == 0 {
		return 0, io.EOF
	}
	step := &b.steps[0]
	n := copy(p, step.data)
	step.data = step.data[n:]
	if len(step.data) > 0 {
		return n, nil
	}
	err := step.err
	b.steps = b.steps[1:]
	return n, err
}
func (b *responseTestBody) Close() error { b.closes++; return nil }
func responseFixtureClient(body io.ReadCloser) *http.Client {
	return &http.Client{Transport: metricsQueryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: body}, nil
	})}
}
func finiteFixtureDocument(mode string) string {
	switch mode {
	case "vector":
		return `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1.000000001,"2"]}]}}`
	case "matrix":
		return `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1.000000001,"NaN"],[2,"+Inf"],[3,"-Inf"]]}]}}`
	case "labels":
		return `{"status":"success","data":["job"]}`
	default:
		return `{"status":"success","data":[{}]}`
	}
}
func callFiniteFixture(mode string, client *http.Client) (string, error) {
	if mode == "vector" || mode == "matrix" {
		r, err := QueryMetrics(context.Background(), MetricsQuery{BaseURL: "http://metrics.test", Scope: testScope(), Bounds: testBounds(), Instant: mode == "vector", client: client})
		return r.Kind, err
	}
	q := MetricsCatalogQuery{BaseURL: "http://metrics.test", Scope: testScope(), Bounds: testBounds(), client: client}
	if mode == "labels" {
		r, err := MetricsLabels(context.Background(), q)
		return r.Kind, err
	}
	r, err := MetricsSeries(context.Background(), q)
	return r.Kind, err
}

func TestMetricResponseFraming(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, suffix string
		valid        bool
	}{
		{"no newline", "", true}, {"ASCII whitespace", " \t\r\n", true},
		{"second object", ` {"private-response-token":1}`, false}, {"null", "null", false}, {"array", "[]", false}, {"garbage", "private-response-token", false},
		{"vertical tab", "\v", false}, {"form feed", "\f", false}, {"NBSP", "\u00a0", false}, {"em space", "\u2003", false},
	}
	for _, mode := range []string{"vector", "matrix", "labels", "series"} {
		for _, item := range cases {
			for _, later := range []bool{false, true} {
				t.Run(mode+"/"+item.name+map[bool]string{false: "/buffered", true: "/later"}[later], func(t *testing.T) {
					doc := finiteFixtureDocument(mode)
					steps := []responseReadStep{{doc + item.suffix, io.EOF}}
					if later {
						steps = []responseReadStep{{doc, nil}, {item.suffix, io.EOF}}
					}
					body := &responseTestBody{steps: steps}
					kind, err := callFiniteFixture(mode, responseFixtureClient(body))
					if (err == nil) != item.valid || (kind != "") != item.valid || body.closes != 1 {
						t.Fatalf("kind=%q err=%v closes=%d valid=%t", kind, err, body.closes, item.valid)
					}
					if err != nil && strings.Contains(err.Error(), "private-response-token") {
						t.Fatalf("response bytes exposed: %v", err)
					}
				})
			}
		}
	}
}

func TestMetricResponseReadErrors(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("scripted response read failure")
	for _, mode := range []string{"vector", "matrix", "labels", "series"} {
		doc := finiteFixtureDocument(mode)
		cases := []struct {
			name  string
			steps []responseReadStep
			want  error
		}{
			{"same read", []responseReadStep{{doc, sentinel}}, sentinel},
			{"later read", []responseReadStep{{doc, nil}, {"", sentinel}}, sentinel},
			{"whitespace and read failure", []responseReadStep{{doc, nil}, {" \t", sentinel}}, sentinel},
			{"same read joined EOF", []responseReadStep{{doc, errors.Join(io.EOF, sentinel)}}, sentinel},
			{"later joined EOF", []responseReadStep{{doc, nil}, {"", errors.Join(io.EOF, sentinel)}}, sentinel},
			{"same read wrapped EOF", []responseReadStep{{doc, fmt.Errorf("read failed: %w", io.EOF)}}, io.EOF},
			{"later wrapped EOF", []responseReadStep{{doc, nil}, {"", fmt.Errorf("read failed: %w", io.EOF)}}, io.EOF},
			{"short document", []responseReadStep{{doc[:len(doc)-1], io.ErrUnexpectedEOF}}, io.ErrUnexpectedEOF},
			{"clean bytes and EOF", []responseReadStep{{doc, io.EOF}}, nil},
			{"split terminal whitespace", []responseReadStep{{doc, nil}, {" ", nil}, {"\t\r", nil}, {"\n", io.EOF}}, nil},
		}
		for _, item := range cases {
			t.Run(mode+"/"+item.name, func(t *testing.T) {
				body := &responseTestBody{steps: item.steps}
				kind, err := callFiniteFixture(mode, responseFixtureClient(body))
				if !errors.Is(err, item.want) || body.closes != 1 || (kind != "") != (item.want == nil) {
					t.Fatalf("kind=%q err=%v want=%v closes=%d", kind, err, item.want, body.closes)
				}
			})
		}
	}
}

// Exact log numbers must survive complete-line validation without float64.
func assertLogNumber(t *testing.T, entry LogEntry) {
	t.Helper()
	number, ok := entry.Raw["n"].(json.Number)
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("log number changed: %#v", entry.Raw)
	}
}

func TestLogResponseFraming(t *testing.T) {
	t.Parallel()
	const row = `{"_msg":"valid","n":9007199254740993}`
	cases := []struct{ name, bad string }{
		{"second object", row + ` {"private-response-token":1}`}, {"null suffix", row + ` null`}, {"array suffix", row + ` []`}, {"garbage suffix", row + `private-response-token`},
		{"leading vertical tab", "\v" + row}, {"trailing form feed", row + "\f"}, {"leading NBSP", "\u00a0" + row}, {"trailing em space", row + "\u2003"}, {"Unicode blank", "\u2003"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			input := row + "\n" + item.bad + "\n" + row + "\n"
			for _, tail := range []bool{false, true} {
				body := &responseTestBody{steps: []responseReadStep{{input, io.EOF}}}
				client := responseFixtureClient(body)
				q := LogsQuery{BaseURL: "http://logs.test", Scope: testScope(), Bounds: testBounds()}
				var err error
				if tail {
					count := 0
					err = tailLogsWithClient(context.Background(), client, q, func(e LogsTailEntry) error { count++; assertLogNumber(t, e.Log); return nil })
					if count != 1 {
						t.Fatalf("tail emitted %d rows instead of valid prefix", count)
					}
				} else {
					result, callErr := queryLogsWithClient(context.Background(), client, q)
					err = callErr
					if result.Kind != "" || len(result.Logs) != 0 {
						t.Fatalf("malformed input became successful envelope: %+v", result)
					}
				}
				if err == nil || strings.Contains(err.Error(), "private-response-token") || body.closes != 1 {
					t.Fatalf("err=%v closes=%d", err, body.closes)
				}
			}
		})
	}
	for _, input := range []string{row, " \t\r\n\n\t " + row + " \t\r\n" + row} {
		body := &responseTestBody{steps: []responseReadStep{{input, io.EOF}}}
		result, err := queryLogsWithClient(context.Background(), responseFixtureClient(body), LogsQuery{BaseURL: "http://logs.test"})
		want := 1
		if strings.Contains(input, "\n") {
			want = 2
		}
		if err != nil || len(result.Logs) != want || body.closes != 1 {
			t.Fatalf("valid lines failed: %+v err=%v closes=%d", result, err, body.closes)
		}
		for _, entry := range result.Logs {
			assertLogNumber(t, entry)
		}
	}
}

func TestLogResponseLifecycle(t *testing.T) {
	t.Parallel()
	const row = `{"_msg":"valid","n":9007199254740993}` + "\n"
	sentinel := errors.New("scripted log read failure")
	for _, end := range []struct {
		name string
		err  error
	}{
		{"clean EOF", io.EOF}, {"read error", sentinel},
		{"joined EOF", errors.Join(io.EOF, sentinel)}, {"wrapped EOF", fmt.Errorf("read failed: %w", io.EOF)},
	} {
		for _, prefix := range []string{"", row} {
			for _, fragment := range []string{strings.TrimSuffix(row, "\n"), row} {
				for _, later := range []bool{false, true} {
					for _, tail := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/prefix%t/LF%t/later%t/tail%t", end.name, prefix != "", fragment == row, later, tail), func(t *testing.T) {
							steps := []responseReadStep{{prefix + fragment, end.err}}
							if later {
								steps = []responseReadStep{{prefix + fragment, nil}, {"", end.err}}
							}
							body := &responseTestBody{steps: steps}
							client := responseFixtureClient(body)
							q := LogsQuery{BaseURL: "http://logs.test"}
							want := end.err
							if end.err == io.EOF { //nolint:errorlint // Only the exact reader sentinel completes a final fragment.
								want = nil
							}
							var err error
							if tail {
								count, wantRows := 0, 0
								if prefix != "" {
									wantRows++
								}
								if fragment == row || want == nil {
									wantRows++
								}
								err = tailLogsWithClient(context.Background(), client, q, func(e LogsTailEntry) error { count++; assertLogNumber(t, e.Log); return nil })
								if count != wantRows {
									t.Fatalf("emitted=%d want completed=%d", count, wantRows)
								}
							} else {
								result, callErr := queryLogsWithClient(context.Background(), client, q)
								err = callErr
								if (result.Kind != "") != (want == nil) || want != nil && len(result.Logs) != 0 {
									t.Fatalf("read failure became success: %+v", result)
								}
							}
							if !errors.Is(err, want) || body.closes != 1 {
								t.Fatalf("err=%v want=%v closes=%d", err, want, body.closes)
							}
						})
					}
				}
			}
		}
	}
	body := &responseTestBody{steps: []responseReadStep{{row + row, io.EOF}}}
	count := 0
	err := tailLogsWithClient(context.Background(), responseFixtureClient(body), LogsQuery{BaseURL: "http://logs.test"}, func(LogsTailEntry) error { count++; return sentinel })
	if !errors.Is(err, sentinel) || count != 1 || body.closes != 1 {
		t.Fatalf("callback err=%v count=%d closes=%d", err, count, body.closes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: metricsQueryTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	err = tailLogsWithClient(ctx, client, LogsQuery{BaseURL: "http://logs.test"}, func(LogsTailEntry) error { t.Fatal("callback after canceled request"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
}

func TestLogStructuredFieldsPreserveNumbers(t *testing.T) {
	t.Parallel()
	exact := map[string]any{
		"n": json.Number("9007199254740993"), "negative": json.Number("-9007199254740993"),
		"decimal": json.Number("1.000000000000000001"), "exponent": json.Number("1e309"),
		"nested": []any{json.Number("9007199254740993"), map[string]any{"number": json.Number("1e-309")}},
		"string": "text", "boolean": true, "null": nil,
	}
	encoded, err := json.Marshal(exact)
	if err != nil {
		t.Fatal(err)
	}
	for _, rawFields := range []string{string(encoded), "", "{}", "null", "[]", `"text"`, `{"n":1} null`, `{"n":1}garbage`, "\u00a0{}"} {
		for _, selected := range []bool{false, true} {
			for _, tail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%q/selected%t/tail%t", rawFields, selected, tail), func(t *testing.T) {
					row, marshalErr := json.Marshal(map[string]any{"_msg": "structured", "fields_json": rawFields})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					body := &responseTestBody{steps: []responseReadStep{{string(row), io.EOF}}}
					client := responseFixtureClient(body)
					q := LogsQuery{BaseURL: "http://logs.test"}
					if selected {
						q.Fields = []string{"_msg"}
					}
					var entries []LogEntry
					var queryErr error
					if tail {
						queryErr = tailLogsWithClient(context.Background(), client, q, func(e LogsTailEntry) error { entries = append(entries, e.Log); return nil })
					} else {
						result, err := queryLogsWithClient(context.Background(), client, q)
						entries, queryErr = result.Logs, err
					}
					if queryErr != nil || len(entries) != 1 || body.closes != 1 {
						t.Fatalf("entries=%v err=%v closes=%d", entries, queryErr, body.closes)
					}
					var want map[string]any
					if rawFields == string(encoded) {
						want = exact
					}
					if !reflect.DeepEqual(entries[0].Fields, want) {
						t.Fatalf("structured fields changed: %#v want %#v", entries[0].Fields, want)
					}
					if want != nil {
						actual, err := json.Marshal(entries[0].Fields)
						if err != nil || string(actual) != string(encoded) {
							t.Fatalf("numeric serialization changed: %s err=%v", actual, err)
						}
					}
					if selected && (len(entries[0].Raw) != 1 || entries[0].Raw["_msg"] != "structured") {
						t.Fatalf("raw selection changed: %#v", entries[0].Raw)
					}
				})
			}
		}
	}
}
